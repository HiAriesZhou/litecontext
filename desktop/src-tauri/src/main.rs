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
    client: Mutex<Option<serde_json::Value>>,
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
            .map_err(|_| reader::ReadError::new("busy", "正在处理，请稍候。"))?;
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
    let core = core_path()?;
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

fn core_path() -> Result<PathBuf, reader::ReadError> {
    let executable = std::env::current_exe()
        .map_err(|_| reader::ReadError::new("core_unavailable", "无法找到应用内的读取程序。"))?;
    let core = executable
        .parent()
        .ok_or_else(|| reader::ReadError::new("core_unavailable", "无法找到应用内的读取程序。"))?
        .join(if cfg!(windows) { "lc.exe" } else { "lc" });
    Ok(core)
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
    *state
        .client
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))? = None;
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

#[tauri::command]
async fn change_memory(
    state: tauri::State<'_, Workspace>,
    bundle_path: String,
    edit: reader::MemoryEdit,
) -> Result<serde_json::Value, reader::ReadError> {
    let _busy = state.enter()?;
    let path = state
        .selected
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))?
        .clone()
        .ok_or_else(|| reader::ReadError::new("no_bundle", "请先选择一个 bundle。"))?;
    // The caller identifies the report it saw, never chooses the write destination.
    if path.to_string_lossy() != bundle_path {
        return Err(reader::ReadError::new(
            "conflict",
            "当前 bundle 已变化，请刷新。",
        ));
    }
    let core = core_path()?;
    let processes = state.processes.clone();
    tauri::async_runtime::spawn_blocking(move || reader::mutate(&core, &path, &processes, edit))
        .await
        .map_err(|_| {
            reader::ReadError::new(
                "write_unknown",
                "操作结果尚未确认，请刷新后检查，勿重复提交。",
            )
        })?
}

async fn client_call(
    state: &Workspace,
    request: serde_json::Value,
) -> Result<serde_json::Value, reader::ReadError> {
    let core = core_path()?;
    let processes = state.processes.clone();
    tauri::async_runtime::spawn_blocking(move || {
        reader::client_operation(&core, &processes, &request)
    })
    .await
    .map_err(|_| reader::ReadError::new("config_unknown", "配置结果尚未确认，请重新预览。"))?
}

#[tauri::command]
async fn preview_client(
    state: tauri::State<'_, Workspace>,
    host: String,
    choose: bool,
) -> Result<Option<serde_json::Value>, reader::ReadError> {
    let _busy = state.enter()?;
    if !["codex", "claude", "gemini"].contains(&host.as_str()) {
        return Err(reader::ReadError::new(
            "config_invalid",
            "不支持这个客户端。",
        ));
    }
    let bundle = state
        .selected
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))?
        .clone()
        .ok_or_else(|| reader::ReadError::new("no_bundle", "请先选择 bundle。"))?;
    let previous = state
        .client
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))?
        .clone();
    let project = if choose || previous.is_none() {
        let Some(folder) = rfd::AsyncFileDialog::new()
            .set_title("选择要接入的项目目录")
            .pick_folder()
            .await
        else {
            return Ok(None);
        };
        folder.path().to_string_lossy().into_owned()
    } else {
        previous
            .as_ref()
            .and_then(|v| v["project"].as_str())
            .ok_or_else(|| reader::ReadError::new("config_invalid", "请重新选择项目。"))?
            .to_owned()
    };
    let request = serde_json::json!({"version":1,"op":"preview","host":host,"project":project,"bundle":bundle.to_string_lossy(),"executable":core_path()?.to_string_lossy()});
    // Save selection even when parsing fails, but clear the previous write precondition.
    *state
        .client
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))? = Some(request.clone());
    let plan = client_call(&state, request.clone()).await?;
    let mut selected = request;
    selected["project"] = plan["project"].clone();
    selected["expected"] = plan["expected"].clone();
    *state
        .client
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))? = Some(selected);
    Ok(Some(plan))
}

#[tauri::command]
async fn change_client(
    state: tauri::State<'_, Workspace>,
    action: String,
) -> Result<serde_json::Value, reader::ReadError> {
    let _busy = state.enter()?;
    if !["apply", "restore"].contains(&action.as_str()) {
        return Err(reader::ReadError::new("config_invalid", "不支持这个操作。"));
    }
    let mut request = {
        let mut slot = state
            .client
            .lock()
            .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))?;
        let selected = slot
            .as_mut()
            .ok_or_else(|| reader::ReadError::new("config_invalid", "请先预览配置。"))?;
        if !selected["expected"].is_string() {
            return Err(reader::ReadError::new(
                "config_invalid",
                "请先重新预览配置。",
            ));
        }
        let request = selected.clone();
        selected.as_object_mut().unwrap().remove("expected");
        request
    };
    request["op"] = serde_json::Value::String(action);
    let plan = client_call(&state, request.clone()).await?;
    request["expected"] = plan["expected"].clone();
    *state
        .client
        .lock()
        .map_err(|_| reader::ReadError::new("state", "请重新打开应用。"))? = Some(request);
    Ok(plan)
}

fn main() {
    tauri::Builder::default()
        .manage(Workspace::default())
        .invoke_handler(tauri::generate_handler![
            choose_bundle,
            refresh_bundle,
            change_memory,
            preview_client,
            change_client
        ])
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
