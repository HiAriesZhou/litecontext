use serde::Serialize;
use serde_json::Value;
use std::{
    io::{Read, Write},
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
    run(
        core,
        bundle,
        processes,
        deadline,
        limit,
        CoreOperation::Inspect,
    )
    .and_then(|bytes| validate(&bytes))
}

enum CoreOperation {
    Inspect,
    Memory(Vec<u8>),
    Client(Vec<u8>),
}

fn run(
    core: &Path,
    bundle: &Path,
    processes: &ProcessSlot,
    deadline: Duration,
    limit: usize,
    operation: CoreOperation,
) -> Result<Vec<u8>, ReadError> {
    let (client, request) = match operation {
        CoreOperation::Inspect => (false, None),
        CoreOperation::Memory(input) => (false, Some(input)),
        CoreOperation::Client(input) => (true, Some(input)),
    };
    let (stdout, stderr, input) = {
        let mut slot = processes
            .0
            .lock()
            .map_err(|_| ReadError::new("state", "请重新打开应用。"))?;
        if slot.closing {
            return Err(ReadError::new("closed", "应用正在关闭。"));
        }
        let mut command = Command::new(core);
        if client {
            command.arg("client-config");
        } else if request.is_some() {
            command.args(["serve", "--once"]);
        } else {
            command.arg("inspect");
        }
        if !client {
            command.arg("--bundle").arg(bundle);
        }
        if request.is_none() {
            command.arg("--json");
        }
        let mut child = command
            .stdin(if request.is_some() {
                Stdio::piped()
            } else {
                Stdio::null()
            })
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|_| {
                ReadError::new(
                    "core_unavailable",
                    "无法启动应用内的读取程序，请重新构建或安装应用。",
                )
            })?;
        let streams = (
            child.stdout.take().unwrap(),
            child.stderr.take().unwrap(),
            child.stdin.take(),
        );
        slot.child = Some(child);
        streams
    };
    let _cleanup = Cleanup(processes);
    let (input_send, input_receive) = mpsc::channel();
    if let (Some(mut input), Some(request)) = (input, request) {
        thread::spawn(move || {
            let _ = input_send.send(input.write_all(&request));
        });
    } else {
        let _ = input_send.send(Ok(()));
    }
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
    let mut input_done = false;
    loop {
        if !input_done {
            if let Ok(result) = input_receive.try_recv() {
                result.map_err(|_| {
                    ReadError::new(
                        "write_unknown",
                        "操作结果尚未确认，请刷新后检查，勿重复提交。",
                    )
                })?;
                input_done = true;
            }
        }
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
                if input_done {
                    return Ok(output.clone());
                }
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

#[derive(serde::Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MemoryEdit {
    pub action: String,
    pub expected: Value,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub replacement_id: String,
}

fn memory_request(edit: MemoryEdit) -> Result<Vec<u8>, ReadError> {
    let old = &edit.expected;
    if !old.is_object() || !old["id"].is_string() || !old["status"].is_string() {
        return Err(ReadError::new("invalid_change", "记忆记录不完整，请刷新。"));
    }
    let mut change = serde_json::json!({"action":edit.action,"id":old["id"],"expected":old});
    match edit.action.as_str() {
        "confirm" | "revoke" => {}
        "correct" => {
            change["text"] = Value::String(edit.text);
        }
        "supersede" => {
            change["id"] = Value::String(edit.replacement_id);
            change["supersedes"] = old["id"].clone();
            change["text"] = Value::String(edit.text);
            for key in ["scope", "visibility", "evidence"] {
                change[key] = old[key].clone();
            }
        }
        _ => return Err(ReadError::new("invalid_change", "不支持这个记忆操作。")),
    }
    let mut bytes =
        serde_json::to_vec(&serde_json::json!({"version":1,"op":"memory","memory":change}))
            .map_err(|_| ReadError::new("invalid_change", "无法准备记忆操作。"))?;
    if bytes.len() > 64 * 1024 {
        return Err(ReadError::new("invalid_change", "记忆内容超过上限。"));
    }
    bytes.push(b'\n');
    Ok(bytes)
}

pub fn mutate(
    core: &Path,
    bundle: &Path,
    processes: &ProcessSlot,
    edit: MemoryEdit,
) -> Result<Value, ReadError> {
    let request = memory_request(edit)?;
    let unknown = || {
        ReadError::new(
            "write_unknown",
            "操作结果尚未确认，请刷新后检查，勿重复提交。",
        )
    };
    let bytes = run(
        core,
        bundle,
        processes,
        Duration::from_secs(10),
        MAX_REPORT,
        CoreOperation::Memory(request),
    )
    .map_err(|_| unknown())?;
    let response: Value = serde_json::from_slice(&bytes).map_err(|_| unknown())?;
    if response["version"] != 1 || response["op"] != "memory" {
        return Err(unknown());
    }
    if response["ok"] == false {
        return Err(match response["error"].as_str() {
            Some("memory changed; refresh before retrying") => {
                ReadError::new("conflict", "这条记忆已被其它操作修改。请刷新后重新检查。")
            }
            Some("memory capacity reached") => ReadError::new(
                "capacity",
                "已达到记忆管理的 50 条记录上限，未创建替代，旧记忆保持不变。",
            ),
            Some("invalid memory") | Some("invalid request") => {
                ReadError::new("invalid_change", "操作未被接受，请检查内容和记忆状态。")
            }
            _ => unknown(),
        });
    }
    if response["ok"] != true || !response["result"].is_object() {
        return Err(unknown());
    }
    Ok(response["result"].clone())
}

#[cfg(test)]
mod memory_tests {
    use super::*;
    fn edit(action: &str) -> MemoryEdit {
        MemoryEdit {
            action: action.into(),
            text: "New \"rule\"\nline".into(),
            replacement_id: "new-rule".into(),
            expected: serde_json::json!({"id":"rule","status":"active","text":"Old","scope":"project","visibility":"private","evidence":[{"sourceId":"src_test","itemId":"itm_test"}]}),
        }
    }
    #[test]
    fn replacement_preserves_metadata_and_expected_record() {
        let request: Value =
            serde_json::from_slice(&memory_request(edit("supersede")).unwrap()).unwrap();
        let memory = &request["memory"];
        assert_eq!(memory["id"], "new-rule");
        assert_eq!(memory["supersedes"], "rule");
        assert_eq!(memory["evidence"], memory["expected"]["evidence"]);
        assert_eq!(memory["visibility"], "private");
        assert_eq!(
            memory_request(edit("propose")).unwrap_err().code,
            "invalid_change"
        );
    }
    #[test]
    #[cfg(unix)]
    fn writes_one_json_request_and_distinguishes_conflict_from_uncertain_result() {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        let core = dir.path().join("lc");
        for (response, expected) in [
            (
                r#"{"version":1,"op":"memory","ok":true,"result":{"id":"rule"}}"#,
                "ok",
            ),
            (
                r#"{"version":1,"op":"memory","ok":false,"error":"memory changed; refresh before retrying"}"#,
                "conflict",
            ),
            (
                r#"{"version":1,"op":"memory","ok":false,"error":"request failed"}"#,
                "write_unknown",
            ),
            ("{}", "write_unknown"),
        ] {
            std::fs::write(&core,format!("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\ncat > \"$0.input\"\nprintf '%s' '{response}'\n")).unwrap();
            std::fs::set_permissions(&core, std::fs::Permissions::from_mode(0o700)).unwrap();
            let slot = ProcessSlot::default();
            let result = mutate(
                &core,
                Path::new("/tmp/中文 bundle's $(literal)"),
                &slot,
                edit("revoke"),
            );
            if expected == "ok" {
                assert!(result.is_ok());
            } else {
                assert_eq!(result.unwrap_err().code, expected);
            }
            assert!(slot.0.lock().unwrap().child.is_none());
            assert_eq!(
                std::fs::read_to_string(core.with_extension("args")).unwrap(),
                "serve\n--once\n--bundle\n/tmp/中文 bundle's $(literal)\n"
            );
            let input = std::fs::read(core.with_extension("input")).unwrap();
            let parsed: Value = serde_json::from_slice(&input).unwrap();
            assert_eq!(parsed["memory"]["expected"]["id"], "rule");
        }
    }
}

// Paths and expected state in this request are assembled by the native picker
// flow. The webview cannot provide an executable or destination to this runner.
pub fn client_operation(
    core: &Path,
    processes: &ProcessSlot,
    request: &Value,
) -> Result<Value, ReadError> {
    let mut bytes = serde_json::to_vec(request)
        .map_err(|_| ReadError::new("config_failed", "无法准备配置。"))?;
    if bytes.len() > 64 * 1024 {
        return Err(ReadError::new("config_failed", "配置请求过大。"));
    }
    bytes.push(b'\n');
    let unknown = || {
        ReadError::new(
            "config_unknown",
            "配置操作结果尚未确认，请重新预览后检查，勿重复提交。",
        )
    };
    let output = run(
        core,
        Path::new(""),
        processes,
        Duration::from_secs(10),
        MAX_REPORT,
        CoreOperation::Client(bytes),
    )
    .map_err(|_| unknown())?;
    let response: Value = serde_json::from_slice(&output).map_err(|_| unknown())?;
    if response["version"] != 1 {
        return Err(unknown());
    }
    if response["ok"] == true && response["result"].is_object() {
        return Ok(response["result"].clone());
    }
    Err(match response["error"].as_str() {
        Some("configuration changed; preview again") => ReadError::new(
            "config_conflict",
            "配置已被其它操作改动。请重新预览；不会覆盖这些改动。",
        ),
        Some("existing integration must be restored before changing it") => ReadError::new(
            "config_managed",
            "已有本机接入记录。请先撤销原接入，再更换配置。",
        ),
        Some("unsupported or invalid configuration") => ReadError::new(
            "config_invalid",
            "无法安全处理此配置。请检查 JSON 格式、链接文件和所选路径。",
        ),
        _ => unknown(),
    })
}

#[cfg(test)]
mod client_tests {
    use super::*;
    #[test]
    #[cfg(unix)]
    fn client_runner_uses_fixed_command_and_requires_versioned_result() {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        let core = dir.path().join("lc");
        for (reply, code) in [
            (
                r#"{"version":1,"ok":true,"result":{"state":"installed"}}"#,
                "ok",
            ),
            (
                r#"{"version":1,"ok":false,"error":"configuration changed; preview again"}"#,
                "config_conflict",
            ),
            (r#"{"ok":true,"result":{}}"#, "config_unknown"),
        ] {
            std::fs::write(&core,format!("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\ncat > \"$0.input\"\nprintf '%s' '{reply}'\n")).unwrap();
            std::fs::set_permissions(&core, std::fs::Permissions::from_mode(0o700)).unwrap();
            let slot = ProcessSlot::default();
            let request = serde_json::json!({"version":1,"op":"preview","host":"codex","project":"/tmp/quoted ' project"});
            let result = client_operation(&core, &slot, &request);
            if code == "ok" {
                assert!(result.is_ok());
            } else {
                assert_eq!(result.unwrap_err().code, code);
            }
            assert_eq!(
                std::fs::read_to_string(core.with_extension("args")).unwrap(),
                "client-config\n"
            );
            let sent: Value =
                serde_json::from_slice(&std::fs::read(core.with_extension("input")).unwrap())
                    .unwrap();
            assert_eq!(sent, request);
            assert!(slot.0.lock().unwrap().child.is_none());
        }
    }
}
