use serde::Serialize;
use serde_json::Value;
use std::{
    io::Read,
    path::Path,
    process::{Child, Command, Stdio},
    sync::{mpsc, Mutex},
    thread,
    time::{Duration, Instant},
};

const MAX_REPORT: usize = 16 * 1024 * 1024;
const MAX_ERROR: usize = 64 * 1024;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ReadError {
    pub code: &'static str,
    pub message: &'static str,
    pub bundle_path: Option<String>,
}
impl ReadError {
    pub fn new(code: &'static str, message: &'static str) -> Self {
        Self {
            code,
            message,
            bundle_path: None,
        }
    }
}

fn bounded<R: Read>(stream: R, limit: usize) -> Result<Vec<u8>, ReadError> {
    let mut output = Vec::new();
    stream
        .take((limit + 1) as u64)
        .read_to_end(&mut output)
        .map_err(|_| ReadError::new("read_failed", "读取中断，请重试。"))?;
    if output.len() > limit {
        return Err(ReadError::new(
            "too_large",
            "报告超过读取上限，请缩小数据范围后重试。",
        ));
    }
    Ok(output)
}

#[derive(Default)]
struct Slot {
    closing: bool,
    child: Option<Child>,
}

#[derive(Default)]
pub struct ProcessSlot(Mutex<Slot>);
impl ProcessSlot {
    fn terminate(&self) {
        if let Ok(mut slot) = self.0.lock() {
            if let Some(mut child) = slot.child.take() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
    }
    pub fn close(&self) {
        if let Ok(mut slot) = self.0.lock() {
            slot.closing = true;
            if let Some(mut child) = slot.child.take() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
    }
}

struct Cleanup<'a>(&'a ProcessSlot);
impl Drop for Cleanup<'_> {
    fn drop(&mut self) {
        self.0.terminate();
    }
}

fn validate(bytes: &[u8]) -> Result<Value, ReadError> {
    let report: Value = serde_json::from_slice(bytes)
        .map_err(|_| ReadError::new("invalid_report", "读取结果无法识别，请检查应用是否完整。"))?;
    let arrays = [
        "sources",
        "memories",
        "currentMemories",
        "events",
        "deliveries",
        "failures",
    ];
    if report["generation"].as_u64().is_none() || arrays.iter().any(|key| !report[*key].is_array())
    {
        return Err(ReadError::new(
            "invalid_report",
            "读取结果不完整，请检查应用是否完整。",
        ));
    }
    Ok(report)
}

pub fn read(core: &Path, bundle: &Path, processes: &ProcessSlot) -> Result<Value, ReadError> {
    read_with_limits(core, bundle, processes, Duration::from_secs(10), MAX_REPORT)
}

fn read_with_limits(
    core: &Path,
    bundle: &Path,
    processes: &ProcessSlot,
    deadline: Duration,
    limit: usize,
) -> Result<Value, ReadError> {
    let (stdout, stderr) = {
        let mut slot = processes
            .0
            .lock()
            .map_err(|_| ReadError::new("state", "请重新打开应用。"))?;
        if slot.closing {
            return Err(ReadError::new("closed", "应用正在关闭。"));
        }
        let mut child = Command::new(core)
            .arg("inspect")
            .arg("--bundle")
            .arg(bundle)
            .arg("--json")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|_| {
                ReadError::new(
                    "core_unavailable",
                    "无法启动应用内的读取程序，请重新构建或安装应用。",
                )
            })?;
        let streams = (child.stdout.take().unwrap(), child.stderr.take().unwrap());
        slot.child = Some(child);
        streams
    };
    let _cleanup = Cleanup(processes);
    let (out_send, out_receive) = mpsc::channel();
    let (err_send, err_receive) = mpsc::channel();
    // Pipe readers finish when the bounded core exits or is killed. They never
    // access the bundle. Only the packaged Go process performs inspection.
    thread::spawn(move || {
        let _ = out_send.send(bounded(stdout, limit));
    });
    thread::spawn(move || {
        let _ = err_send.send(bounded(stderr, MAX_ERROR));
    });
    let start = Instant::now();
    let (mut output, mut errors) = (None, None);
    loop {
        if output.is_none() {
            if let Ok(value) = out_receive.try_recv() {
                output = Some(value?);
            }
        }
        if errors.is_none() {
            if let Ok(value) = err_receive.try_recv() {
                errors = Some(value?);
            }
        }
        let status = {
            let mut slot = processes
                .0
                .lock()
                .map_err(|_| ReadError::new("state", "请重新打开应用。"))?;
            let child = slot
                .child
                .as_mut()
                .ok_or_else(|| ReadError::new("closed", "应用正在关闭。"))?;
            child
                .try_wait()
                .map_err(|_| ReadError::new("read_failed", "读取程序意外退出，请重试。"))?
        };
        if let Some(status) = status {
            if !status.success() {
                return Err(ReadError::new(
                    "bundle_unavailable",
                    "无法读取这个 bundle。请选择有效的 LiteContext bundle，或在文件恢复后刷新。",
                ));
            }
            if let (Some(output), Some(_)) = (&output, &errors) {
                return validate(output);
            }
        }
        if start.elapsed() >= deadline {
            return Err(ReadError::new(
                "timeout",
                "读取超过时限，请确认 bundle 可用后重试。",
            ));
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bounded_output_rejects_overflow() {
        assert_eq!(bounded(&b"abc"[..], 3).unwrap(), b"abc");
        assert_eq!(bounded(&b"abcd"[..], 3).unwrap_err().code, "too_large");
    }

    #[test]
    fn malformed_or_incomplete_reports_are_rejected() {
        for input in [b"garbage".as_slice(), b"{}", b"{\"generation\":0}", b"[]"] {
            assert_eq!(validate(input).unwrap_err().code, "invalid_report");
        }
        let valid = br#"{"generation":0,"sources":[],"memories":[],"currentMemories":[],"events":[],"deliveries":[],"failures":[]}"#;
        assert!(validate(valid).is_ok());
    }

    #[cfg(unix)]
    fn fake_core(script: &str) -> (tempfile::TempDir, std::path::PathBuf) {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("lc");
        std::fs::write(&path, format!("#!/bin/sh\n{script}\n")).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o700)).unwrap();
        (dir, path)
    }

    #[test]
    #[cfg(unix)]
    fn passes_paths_as_literal_arguments_and_reaps_success() {
        let (_dir, core) = fake_core(
            r#"printf '%s\n' "$@" > "$0.args"
printf '%s' '{"generation":0,"sources":[],"memories":[],"currentMemories":[],"events":[],"deliveries":[],"failures":[]}'"#,
        );
        let slot = ProcessSlot::default();
        let path = "/tmp/中文 bundle's $(do-not-execute)";
        assert!(read(&core, Path::new(path), &slot).is_ok());
        assert_eq!(
            std::fs::read_to_string(core.with_extension("args")).unwrap(),
            format!("inspect\n--bundle\n{path}\n--json\n")
        );
        assert!(slot.0.lock().unwrap().child.is_none());
    }

    #[test]
    #[cfg(unix)]
    fn timeout_output_overflow_and_failure_leave_no_child() {
        for (script, expected) in [
            ("exec sleep 10", "timeout"),
            ("printf '%01000d' 0", "too_large"),
            ("exit 7", "bundle_unavailable"),
            ("printf '{}'", "invalid_report"),
        ] {
            let (_dir, core) = fake_core(script);
            let slot = ProcessSlot::default();
            // A freshly written executable can incur macOS first-run checks.
            // Only the timeout case should race a deliberately short deadline.
            let deadline = if expected == "timeout" {
                Duration::from_millis(150)
            } else {
                Duration::from_secs(3)
            };
            let error =
                read_with_limits(&core, Path::new("/unused"), &slot, deadline, 128).unwrap_err();
            assert_eq!(error.code, expected);
            assert!(slot.0.lock().unwrap().child.is_none());
        }
    }

    #[test]
    #[cfg(unix)]
    fn closing_kills_active_read_and_prevents_another_spawn() {
        use std::sync::Arc;
        let (_dir, core) = fake_core("exec sleep 10");
        let slot = Arc::new(ProcessSlot::default());
        let worker_slot = slot.clone();
        let worker_core = core.clone();
        let worker = thread::spawn(move || read(&worker_core, Path::new("/unused"), &worker_slot));
        let start = Instant::now();
        while slot.0.lock().unwrap().child.is_none() {
            assert!(start.elapsed() < Duration::from_secs(3));
            thread::sleep(Duration::from_millis(5));
        }
        slot.close();
        assert_eq!(worker.join().unwrap().unwrap_err().code, "closed");
        assert!(slot.0.lock().unwrap().child.is_none());
        assert_eq!(
            read(&core, Path::new("/unused"), &slot).unwrap_err().code,
            "closed"
        );
    }
}
