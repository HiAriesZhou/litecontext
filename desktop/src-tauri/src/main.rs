#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod reader;

use serde::Serialize;
use std::{
    path::PathBuf,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
};
use tauri::Manager;

#[derive(Default)]
struct Workspace {
    selected: Mutex<Option<PathBuf>>,
    busy: AtomicBool,
    processes: Arc<reader::ProcessSlot>,
}

struct Busy<'a>(&'a AtomicBool);
impl Drop for Busy<'_> {
    fn drop(&mut self) {
        self.0.store(false, Ordering::Release);
    }
}
impl Workspace {
    fn enter(&self) -> Result<Busy<'_>, reader::ReadError> {
        self.busy
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .map_err(|_| reader::ReadError::new("busy", "正在读取，请稍候。"))?;
        Ok(Busy(&self.busy))
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Snapshot {
    bundle_path: String,
    report: serde_json::Value,
    elapsed_ms: u128,
}

async fn inspect(
    path: PathBuf,
    processes: Arc<reader::ProcessSlot>,
) -> Result<Snapshot, reader::ReadError> {
    let start = std::time::Instant::now();
    let executable = std::env::current_exe()
        .map_err(|_| reader::ReadError::new("core_unavailable", "无法找到应用内的读取程序。"))?;
    let core = executable
        .parent()
        .ok_or_else(|| reader::ReadError::new("core_unavailable", "无法找到应用内的读取程序。"))?
        .join(if cfg!(windows) { "lc.exe" } else { "lc" });
    let read_path = path.clone();
    let report =
        tauri::async_runtime::spawn_blocking(move || reader::read(&core, &read_path, &processes))
            .await
            .map_err(|_| reader::ReadError::new("read_failed", "读取程序意外退出，请重试。"))?
            .map_err(|mut error| {
                error.bundle_path = Some(path.to_string_lossy().into_owned());
                error
            })?;
    Ok(Snapshot {
        bundle_path: path.to_string_lossy().into_owned(),
        report,
        elapsed_ms: start.elapsed().as_millis(),
    })
}

#[tauri::command]
async fn choose_bundle(
    state: tauri::State<'_, Workspace>,
) -> Result<Option<Snapshot>, reader::ReadError> {
    let _busy = state.enter()?;
    let Some(folder) = rfd::AsyncFileDialog::new()
        .set_title("选择 LiteContext bundle 文件夹")
        .pick_folder()
        .await
    else {
        return Ok(None);
    };
    let path = folder.path().to_path_buf();
    *state
        .selected
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))? = Some(path.clone());
    inspect(path, state.processes.clone()).await.map(Some)
}

#[tauri::command]
async fn refresh_bundle(state: tauri::State<'_, Workspace>) -> Result<Snapshot, reader::ReadError> {
    let _busy = state.enter()?;
    let path = state
        .selected
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))?
        .clone()
        .ok_or_else(|| reader::ReadError::new("no_bundle", "请先选择一个 bundle。"))?;
    inspect(path, state.processes.clone()).await
}

fn main() {
    tauri::Builder::default()
        .manage(Workspace::default())
        .invoke_handler(tauri::generate_handler![choose_bundle, refresh_bundle])
        .build(tauri::generate_context!())
        .expect("could not start LiteContext")
        .run(|app, event| {
            if matches!(
                event,
                tauri::RunEvent::ExitRequested { .. } | tauri::RunEvent::Exit
            ) {
                app.state::<Workspace>().processes.close();
            }
        });
}
