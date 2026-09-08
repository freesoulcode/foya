#![cfg(unix)]

use std::collections::HashMap;
use std::fs;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Output, Stdio};
use std::sync::{Mutex, OnceLock};
use std::thread;
use std::time::{Duration, Instant};

use flate2::read::GzDecoder;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tauri::{AppHandle, Manager};

const REMOTE_BASE: &str = "$HOME/.local/share/foya";
const SSH_CONNECT_TIMEOUT_SECONDS: &str = "10";
const REMOTE_KERNEL_RELEASE_BASE: &str = "https://github.com/freesoulcode/foya/releases/download";
const REMOTE_KERNEL_DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(120);
const MAX_COMPRESSED_REMOTE_KERNEL_BYTES: u64 = 64 << 20;
const MAX_REMOTE_KERNEL_BYTES: u64 = 128 << 20;

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct SshConnection {
    pub(crate) name: String,
    pub(crate) target: String,
    #[serde(default = "default_ssh_port")]
    pub(crate) port: u16,
    #[serde(default = "default_remote_port")]
    pub(crate) remote_port: u16,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct SshHostInput {
    #[serde(default)]
    pub(crate) name: String,
    pub(crate) target: String,
    #[serde(default = "default_ssh_port")]
    pub(crate) port: u16,
}

#[derive(Clone, Debug, Serialize)]
pub(crate) struct SshHostProbe {
    pub(crate) os: String,
    pub(crate) architecture: String,
    pub(crate) sandbox_available: bool,
}

#[derive(Clone, Debug, Serialize)]
pub(crate) struct SshConfigHost {
    pub(crate) alias: String,
    pub(crate) hostname: String,
    pub(crate) user: String,
    pub(crate) port: u16,
}

struct SshRuntime {
    connection: SshConnection,
    token: String,
    local_port: u16,
    child: Option<Child>,
}

struct RemoteState {
    probe: SshHostProbe,
    binary_hash: String,
    auth_hash: String,
    remote_port: u16,
    running: bool,
}

static SSH_RUNTIME: OnceLock<Mutex<Option<SshRuntime>>> = OnceLock::new();

fn default_ssh_port() -> u16 {
    22
}

fn default_remote_port() -> u16 {
    8787
}

fn runtime() -> &'static Mutex<Option<SshRuntime>> {
    SSH_RUNTIME.get_or_init(|| Mutex::new(None))
}

pub(crate) fn normalize_connection(input: SshHostInput) -> Result<SshConnection, String> {
    let target = input.target.trim();
    if target.is_empty() {
        return Err("SSH host is required".into());
    }
    if target.starts_with('-')
        || !target.bytes().all(|byte| {
            byte.is_ascii_alphanumeric()
                || matches!(byte, b'.' | b'-' | b'_' | b'@' | b':' | b'%' | b'[' | b']')
        })
    {
        return Err("SSH host contains unsupported characters".into());
    }
    if input.port == 0 {
        return Err("SSH port must be between 1 and 65535".into());
    }
    let name = if input.name.trim().is_empty() {
        target.to_string()
    } else {
        input.name.trim().to_string()
    };
    Ok(SshConnection {
        name,
        target: target.to_string(),
        port: input.port,
        remote_port: default_remote_port(),
    })
}

pub(crate) fn list_config_hosts() -> Vec<SshConfigHost> {
    let Some(home) = std::env::var_os("HOME") else {
        return Vec::new();
    };
    let Ok(source) = fs::read_to_string(PathBuf::from(home).join(".ssh").join("config")) else {
        return Vec::new();
    };
    parse_ssh_config(&source)
}

pub(crate) fn probe(connection: &SshConnection) -> Result<SshHostProbe, String> {
    Ok(remote_state(connection)?.probe)
}

pub(crate) fn deploy_and_connect(
    app: &AppHandle,
    connection: &SshConnection,
    token: &str,
) -> Result<(String, SshHostProbe), String> {
    let state = remote_state(connection)?;
    let binary = remote_binary(app, &state.probe.architecture)?;
    deploy_binary_and_connect(connection, token, state, &binary)
}

fn deploy_binary_and_connect(
    connection: &SshConnection,
    token: &str,
    state: RemoteState,
    binary: &[u8],
) -> Result<(String, SshHostProbe), String> {
    if state.probe.os != "Linux" {
        return Err(format!(
            "Remote kernel requires Linux; detected {}",
            state.probe.os
        ));
    }
    if !state.probe.sandbox_available {
        return Err("Remote server requires Bubblewrap (/usr/bin/bwrap)".into());
    }
    let binary_hash = sha256_hex(binary);
    let auth_hash = sha256_hex(token.as_bytes());
    let port_changed = state.remote_port != connection.remote_port;
    let binary_changed = state.binary_hash != binary_hash;
    let auth_changed = state.auth_hash != auth_hash;

    if binary_changed {
        upload_remote_binary(connection, binary)?;
        write_remote_text(
            connection,
            "umask 077; mkdir -p \"$HOME/.local/share/foya\"; cat > \"$HOME/.local/share/foya/binary.sha256\"",
            binary_hash.as_bytes(),
        )?;
    }
    if auth_changed {
        write_remote_text(
            connection,
            "umask 077; mkdir -p \"$HOME/.local/share/foya\"; cat > \"$HOME/.local/share/foya/auth-token.sha256\"",
            auth_hash.as_bytes(),
        )?;
    }
    if binary_changed || auth_changed || port_changed || !state.running {
        restart_remote_kernel(connection)?;
    }

    let url = connect_tunnel(connection, token)?;
    Ok((url, state.probe))
}

pub(crate) fn ensure_tunnel() -> Result<(), String> {
    let mut guard = runtime()
        .lock()
        .map_err(|_| "SSH tunnel state is unavailable".to_string())?;
    let Some(state) = guard.as_mut() else {
        return Err("SSH tunnel is not configured".into());
    };
    if let Some(child) = state.child.as_mut() {
        if child
            .try_wait()
            .map_err(|error| format!("Failed to inspect SSH tunnel: {error}"))?
            .is_none()
        {
            return Ok(());
        }
    }
    state.child = Some(start_tunnel(&state.connection, state.local_port)?);
    let started = Instant::now();
    loop {
        if let Some(child) = state.child.as_mut() {
            if let Some(status) = child
                .try_wait()
                .map_err(|error| format!("Failed to inspect SSH tunnel: {error}"))?
            {
                return Err(format!("SSH tunnel exited with {status}"));
            }
        }
        match check_ready(state.local_port, &state.token) {
            Ok(()) => break,
            Err(error) if started.elapsed() >= Duration::from_secs(5) => {
                if let Some(mut child) = state.child.take() {
                    let _ = child.kill();
                    let _ = child.wait();
                }
                return Err(format!("SSH tunnel did not become ready: {error}"));
            }
            Err(_) => thread::sleep(Duration::from_millis(150)),
        }
    }
    Ok(())
}

pub(crate) fn shutdown() {
    let Ok(mut guard) = runtime().lock() else {
        return;
    };
    if let Some(mut state) = guard.take() {
        if let Some(child) = state.child.as_mut() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

fn remote_state(connection: &SshConnection) -> Result<RemoteState, String> {
    let script = r#"base="$HOME/.local/share/foya"
printf 'os=%s\n' "$(uname -s)"
printf 'arch=%s\n' "$(uname -m)"
if test -x /usr/bin/bwrap; then printf 'sandbox=yes\n'; else printf 'sandbox=no\n'; fi
printf 'binary_hash='; cat "$base/binary.sha256" 2>/dev/null || true; printf '\n'
printf 'auth_hash='; cat "$base/auth-token.sha256" 2>/dev/null || true; printf '\n'
printf 'remote_port='; cat "$base/remote-port" 2>/dev/null || true; printf '\n'
running=no
if test -f "$base/run/kernel.pid"; then
  pid="$(cat "$base/run/kernel.pid" 2>/dev/null || true)"
  if test -n "$pid" && kill -0 "$pid" 2>/dev/null; then
    command="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    case "$command" in *"$base/bin/foya serve"*) running=yes ;; esac
  fi
fi
printf 'running=%s\n' "$running"
"#;
    let output = ssh_output(connection, script)?;
    let fields = parse_key_values(&String::from_utf8_lossy(&output.stdout));
    let os = fields.get("os").cloned().unwrap_or_default();
    let architecture = fields.get("arch").cloned().unwrap_or_default();
    if os.is_empty() || architecture.is_empty() {
        return Err("SSH host did not report its operating system and architecture".into());
    }
    Ok(RemoteState {
        probe: SshHostProbe {
            os,
            architecture,
            sandbox_available: fields.get("sandbox").is_some_and(|value| value == "yes"),
        },
        binary_hash: fields.get("binary_hash").cloned().unwrap_or_default(),
        auth_hash: fields.get("auth_hash").cloned().unwrap_or_default(),
        remote_port: fields
            .get("remote_port")
            .and_then(|value| value.parse().ok())
            .unwrap_or_default(),
        running: fields.get("running").is_some_and(|value| value == "yes"),
    })
}

fn remote_binary(app: &AppHandle, architecture: &str) -> Result<Vec<u8>, String> {
    let asset = match architecture {
        "x86_64" | "amd64" => "foya-linux-amd64.gz",
        "aarch64" | "arm64" => "foya-linux-arm64.gz",
        other => return Err(format!("Unsupported remote Linux architecture: {other}")),
    };

    #[cfg(debug_assertions)]
    {
        let development = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join("remote")
            .join(asset);
        if development.is_file() {
            return load_remote_kernel(&development);
        }
    }

    let version = app.package_info().version.to_string();
    let cache_dir = app
        .path()
        .app_cache_dir()
        .map_err(|error| format!("Unable to resolve remote kernel cache: {error}"))?
        .join("remote-kernels")
        .join(&version);
    let cached = cache_dir.join(asset);
    if cached.is_file() {
        match load_remote_kernel(&cached) {
            Ok(binary) => return Ok(binary),
            Err(_) => {
                let _ = fs::remove_file(&cached);
            }
        }
    }

    let compressed = download_remote_kernel(&version, asset)
        .map_err(|error| format!("Unable to download remote kernel: {error}"))?;
    let binary = decompress_remote_binary(compressed.as_slice())
        .map_err(|error| format!("Invalid downloaded remote kernel: {error}"))?;
    cache_remote_kernel(&cache_dir, asset, &compressed)
        .map_err(|error| format!("Unable to cache remote kernel: {error}"))?;
    Ok(binary)
}

fn load_remote_kernel(path: &Path) -> Result<Vec<u8>, String> {
    let file = fs::File::open(path).map_err(|error| {
        format!(
            "Remote kernel resource is missing at {}: {error}",
            path.display()
        )
    })?;
    decompress_remote_binary(file).map_err(|error| {
        format!(
            "Invalid remote kernel resource at {}: {error}",
            path.display()
        )
    })
}

fn download_remote_kernel(version: &str, asset: &str) -> Result<Vec<u8>, String> {
    let base = format!("{REMOTE_KERNEL_RELEASE_BASE}/v{version}");
    let client = reqwest::blocking::Client::builder()
        .connect_timeout(Duration::from_secs(15))
        .timeout(REMOTE_KERNEL_DOWNLOAD_TIMEOUT)
        .user_agent(format!("Foya/{version}"))
        .build()
        .map_err(|error| format!("Failed to prepare remote kernel download: {error}"))?;
    let checksum = download_bytes(&client, &format!("{base}/{asset}.sha256"), 4096, "checksum")?;
    let expected = parse_release_checksum(&checksum, asset)?;
    let compressed = download_bytes(
        &client,
        &format!("{base}/{asset}"),
        MAX_COMPRESSED_REMOTE_KERNEL_BYTES,
        "binary",
    )?;
    let actual = sha256_hex(&compressed);
    if actual != expected {
        return Err(format!(
            "Remote kernel checksum mismatch: expected {expected}, received {actual}"
        ));
    }
    Ok(compressed)
}

fn download_bytes(
    client: &reqwest::blocking::Client,
    url: &str,
    max_bytes: u64,
    kind: &str,
) -> Result<Vec<u8>, String> {
    let response = client
        .get(url)
        .send()
        .and_then(reqwest::blocking::Response::error_for_status)
        .map_err(|error| format!("Failed to download remote kernel {kind}: {error}"))?;
    if response
        .content_length()
        .is_some_and(|length| length > max_bytes)
    {
        return Err(format!("Remote kernel {kind} exceeds download size limit"));
    }
    let mut limited = response.take(max_bytes + 1);
    let mut bytes = Vec::new();
    limited
        .read_to_end(&mut bytes)
        .map_err(|error| format!("Failed to read remote kernel {kind}: {error}"))?;
    if bytes.len() as u64 > max_bytes {
        return Err(format!("Remote kernel {kind} exceeds download size limit"));
    }
    Ok(bytes)
}

fn parse_release_checksum(source: &[u8], asset: &str) -> Result<String, String> {
    let text = std::str::from_utf8(source)
        .map_err(|_| "Remote kernel checksum is not UTF-8".to_string())?;
    let mut fields = text.split_whitespace();
    let hash = fields
        .next()
        .filter(|value| value.len() == 64 && value.bytes().all(|byte| byte.is_ascii_hexdigit()))
        .ok_or_else(|| "Remote kernel checksum is invalid".to_string())?;
    let listed_asset = fields
        .next()
        .map(|value| value.trim_start_matches('*'))
        .ok_or_else(|| "Remote kernel checksum has no asset name".to_string())?;
    if listed_asset != asset {
        return Err("Remote kernel checksum names a different asset".into());
    }
    Ok(hash.to_ascii_lowercase())
}

fn cache_remote_kernel(cache_dir: &Path, asset: &str, compressed: &[u8]) -> Result<(), String> {
    fs::create_dir_all(cache_dir)
        .map_err(|error| format!("Unable to create remote kernel cache: {error}"))?;
    let destination = cache_dir.join(asset);
    let temporary = cache_dir.join(format!("{asset}.download"));
    fs::write(&temporary, compressed)
        .map_err(|error| format!("Unable to write remote kernel cache: {error}"))?;
    if let Err(error) = fs::rename(&temporary, &destination) {
        let _ = fs::remove_file(&temporary);
        return Err(format!("Unable to activate remote kernel cache: {error}"));
    }
    Ok(())
}

fn decompress_remote_binary(source: impl Read) -> Result<Vec<u8>, String> {
    let decoder = GzDecoder::new(source);
    let mut limited = decoder.take(MAX_REMOTE_KERNEL_BYTES + 1);
    let mut binary = Vec::new();
    limited
        .read_to_end(&mut binary)
        .map_err(|error| format!("Failed to decompress remote kernel: {error}"))?;
    if binary.len() as u64 > MAX_REMOTE_KERNEL_BYTES {
        return Err("Decompressed remote kernel exceeds size limit".into());
    }
    if !binary.starts_with(b"\x7fELF") {
        return Err("Decompressed remote kernel is not a Linux ELF binary".into());
    }
    Ok(binary)
}

fn upload_remote_binary(connection: &SshConnection, binary: &[u8]) -> Result<(), String> {
    write_remote_text(
        connection,
        &format!(
            "set -eu; umask 077; mkdir -p \"{REMOTE_BASE}/bin\"; cat > \"{REMOTE_BASE}/bin/foya.upload\"; chmod 0700 \"{REMOTE_BASE}/bin/foya.upload\"; mv \"{REMOTE_BASE}/bin/foya.upload\" \"{REMOTE_BASE}/bin/foya\""
        ),
        binary,
    )
}

fn restart_remote_kernel(connection: &SshConnection) -> Result<(), String> {
    let script = format!(
        r#"set -eu
base="{REMOTE_BASE}"
mkdir -p "$base/bin" "$base/data" "$base/run"
if test -f "$base/run/kernel.pid"; then
  pid="$(cat "$base/run/kernel.pid" 2>/dev/null || true)"
  if test -n "$pid" && kill -0 "$pid" 2>/dev/null; then
    command="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    case "$command" in
      *"$base/bin/foya serve"*) kill "$pid" 2>/dev/null || true ;;
    esac
    count=0
    while kill -0 "$pid" 2>/dev/null && test "$count" -lt 40; do
      sleep 0.1
      count=$((count + 1))
    done
  fi
fi
printf '%s' '{remote_port}' > "$base/remote-port"
nohup "$base/bin/foya" serve \
  --listen "127.0.0.1:{remote_port}" \
  --data-dir "$base/data" \
  --auth-token-hash-file "$base/auth-token.sha256" \
  --allow-plaintext \
  >"$base/run/kernel.log" 2>&1 </dev/null &
pid=$!
printf '%s' "$pid" > "$base/run/kernel.pid"
sleep 0.5
if ! kill -0 "$pid" 2>/dev/null; then
  tail -n 20 "$base/run/kernel.log" >&2 || true
  exit 1
fi
"#,
        remote_port = connection.remote_port,
    );
    ssh_output(connection, &script).map(|_| ())
}

fn connect_tunnel(connection: &SshConnection, token: &str) -> Result<String, String> {
    shutdown();
    let local_port = available_local_port()?;
    let mut child = start_tunnel(connection, local_port)?;
    let started = Instant::now();
    let ready = loop {
        if let Some(status) = child
            .try_wait()
            .map_err(|error| format!("Failed to inspect SSH tunnel: {error}"))?
        {
            break Err(format!("SSH tunnel exited with {status}"));
        }
        match check_ready(local_port, token) {
            Ok(()) => break Ok(()),
            Err(error) if started.elapsed() >= Duration::from_secs(10) => break Err(error),
            Err(_) => thread::sleep(Duration::from_millis(250)),
        }
    };
    if let Err(error) = ready {
        let _ = child.kill();
        let _ = child.wait();
        return Err(format!("Remote kernel did not become ready: {error}"));
    }
    let mut guard = runtime()
        .lock()
        .map_err(|_| "SSH tunnel state is unavailable".to_string())?;
    *guard = Some(SshRuntime {
        connection: connection.clone(),
        token: token.to_string(),
        local_port,
        child: Some(child),
    });
    Ok(format!("http://127.0.0.1:{local_port}"))
}

fn start_tunnel(connection: &SshConnection, local_port: u16) -> Result<Child, String> {
    let mut command = ssh_command(connection);
    command
        .arg("-N")
        .arg("-o")
        .arg("ExitOnForwardFailure=yes")
        .arg("-o")
        .arg("ServerAliveInterval=15")
        .arg("-o")
        .arg("ServerAliveCountMax=3")
        .arg("-L")
        .arg(format!(
            "127.0.0.1:{local_port}:127.0.0.1:{}",
            connection.remote_port
        ))
        .arg(&connection.target)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    command
        .spawn()
        .map_err(|error| format!("Failed to start SSH tunnel: {error}"))
}

fn check_ready(port: u16, token: &str) -> Result<(), String> {
    let address = SocketAddr::from(([127, 0, 0, 1], port));
    let mut stream = TcpStream::connect_timeout(&address, Duration::from_millis(500))
        .map_err(|error| error.to_string())?;
    let _ = stream.set_read_timeout(Some(Duration::from_secs(1)));
    let request = format!(
        "GET /readyz HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer {token}\r\nConnection: close\r\n\r\n"
    );
    stream
        .write_all(request.as_bytes())
        .map_err(|error| error.to_string())?;
    let mut response = [0; 128];
    let count = stream
        .read(&mut response)
        .map_err(|error| error.to_string())?;
    let status = String::from_utf8_lossy(&response[..count]);
    if status.starts_with("HTTP/1.1 200") {
        Ok(())
    } else {
        Err(status
            .lines()
            .next()
            .unwrap_or("invalid response")
            .to_string())
    }
}

fn available_local_port() -> Result<u16, String> {
    TcpListener::bind("127.0.0.1:0")
        .and_then(|listener| listener.local_addr())
        .map(|address| address.port())
        .map_err(|error| format!("Unable to reserve a local tunnel port: {error}"))
}

fn write_remote_text(
    connection: &SshConnection,
    script: &str,
    content: &[u8],
) -> Result<(), String> {
    let mut command = ssh_command(connection);
    let mut child = command
        .arg(&connection.target)
        .arg(script)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|error| format!("Failed to start SSH upload: {error}"))?;
    let write_result = child
        .stdin
        .take()
        .ok_or_else(|| "SSH upload stdin is unavailable".to_string())
        .and_then(|mut stdin| {
            stdin
                .write_all(content)
                .map_err(|error| format!("Failed to upload remote kernel: {error}"))
        });
    if let Err(error) = write_result {
        let _ = child.kill();
        let _ = child.wait();
        return Err(error);
    }
    let output = child
        .wait_with_output()
        .map_err(|error| format!("Failed to finish SSH upload: {error}"))?;
    check_ssh_output(output).map(|_| ())
}

fn ssh_output(connection: &SshConnection, script: &str) -> Result<Output, String> {
    let output = ssh_command(connection)
        .arg(&connection.target)
        .arg(script)
        .output()
        .map_err(|error| format!("Failed to run SSH: {error}"))?;
    check_ssh_output(output)
}

fn check_ssh_output(output: Output) -> Result<Output, String> {
    if output.status.success() {
        return Ok(output);
    }
    let message = String::from_utf8_lossy(&output.stderr).trim().to_string();
    if message.is_empty() {
        Err(format!("SSH exited with {}", output.status))
    } else {
        Err(message)
    }
}

fn ssh_command(connection: &SshConnection) -> Command {
    let executable = if PathBuf::from("/usr/bin/ssh").is_file() {
        "/usr/bin/ssh"
    } else {
        "ssh"
    };
    let mut command = Command::new(executable);
    command.arg("-T");
    if connection.port != default_ssh_port() {
        command.arg("-p").arg(connection.port.to_string());
    }
    command
        .arg("-o")
        .arg("BatchMode=yes")
        .arg("-o")
        .arg(format!("ConnectTimeout={SSH_CONNECT_TIMEOUT_SECONDS}"))
        .arg("-o")
        .arg("StrictHostKeyChecking=accept-new");
    command
}

fn sha256_hex(content: &[u8]) -> String {
    let digest = Sha256::digest(content);
    digest.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn parse_key_values(source: &str) -> HashMap<String, String> {
    source
        .lines()
        .filter_map(|line| line.split_once('='))
        .map(|(key, value)| (key.trim().to_string(), value.trim().to_string()))
        .collect()
}

fn parse_ssh_config(source: &str) -> Vec<SshConfigHost> {
    let mut result = Vec::new();
    let mut current: Option<SshConfigHost> = None;
    for raw in source.lines() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let mut parts = line.split_whitespace();
        let Some(key) = parts.next() else {
            continue;
        };
        let value = parts.collect::<Vec<_>>().join(" ");
        if key.eq_ignore_ascii_case("Host") {
            if let Some(host) = current.take() {
                result.push(host);
            }
            let alias = value.split_whitespace().next().unwrap_or_default();
            if alias.is_empty()
                || alias
                    .chars()
                    .any(|character| matches!(character, '*' | '?' | '!'))
            {
                current = None;
            } else {
                current = Some(SshConfigHost {
                    alias: alias.to_string(),
                    hostname: String::new(),
                    user: String::new(),
                    port: default_ssh_port(),
                });
            }
            continue;
        }
        let Some(host) = current.as_mut() else {
            continue;
        };
        if key.eq_ignore_ascii_case("HostName") {
            host.hostname = value;
        } else if key.eq_ignore_ascii_case("User") {
            host.user = value;
        } else if key.eq_ignore_ascii_case("Port") {
            host.port = value.parse().unwrap_or(default_ssh_port());
        }
    }
    if let Some(host) = current {
        result.push(host);
    }
    result
}

#[cfg(test)]
mod tests {
    use std::fs;
    use std::io::Write;

    use super::{
        decompress_remote_binary, deploy_binary_and_connect, ensure_tunnel, normalize_connection,
        parse_release_checksum, parse_ssh_config, remote_state, runtime, sha256_hex, shutdown,
        SshConnection, SshHostInput,
    };
    use flate2::write::GzEncoder;
    use flate2::Compression;

    #[test]
    fn parses_concrete_ssh_hosts() {
        let hosts = parse_ssh_config(
            "Host *\n  ServerAliveInterval 15\nHost home\n  HostName 192.168.1.20\n  User dev\n  Port 2222\n",
        );
        assert_eq!(hosts.len(), 1);
        assert_eq!(hosts[0].alias, "home");
        assert_eq!(hosts[0].hostname, "192.168.1.20");
        assert_eq!(hosts[0].user, "dev");
        assert_eq!(hosts[0].port, 2222);
    }

    #[test]
    fn rejects_ssh_option_injection() {
        let result = normalize_connection(SshHostInput {
            name: String::new(),
            target: "-oProxyCommand=bad".into(),
            port: 22,
        });
        assert!(result.is_err());
    }

    #[test]
    fn hashes_tokens_without_storing_them_remotely() {
        assert_eq!(
            sha256_hex(b"0123456789abcdef0123456789abcdef"),
            "3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9"
        );
    }

    #[test]
    fn decompresses_bundled_remote_kernel() {
        let payload = b"\x7fELFtest-kernel";
        let mut encoder = GzEncoder::new(Vec::new(), Compression::best());
        encoder.write_all(payload).expect("compress kernel");
        let compressed = encoder.finish().expect("finish kernel compression");

        assert_eq!(
            decompress_remote_binary(compressed.as_slice()).expect("decompress kernel"),
            payload
        );
        assert!(decompress_remote_binary(b"not-gzip".as_slice()).is_err());
    }

    #[test]
    fn validates_release_checksum_asset_name() {
        let hash = "3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9";
        let source = format!("{hash}  foya-linux-arm64.gz\n");
        assert_eq!(
            parse_release_checksum(source.as_bytes(), "foya-linux-arm64.gz")
                .expect("valid checksum"),
            hash
        );
        assert!(
            parse_release_checksum(source.as_bytes(), "foya-linux-amd64.gz").is_err(),
            "a checksum for another architecture must not be accepted"
        );
    }

    #[test]
    fn deploys_over_ssh_when_integration_host_is_configured() {
        let Ok(target) = std::env::var("FOYA_TEST_SSH_TARGET") else {
            return;
        };
        let port = std::env::var("FOYA_TEST_SSH_PORT")
            .ok()
            .and_then(|value| value.parse().ok())
            .unwrap_or(22);
        let binary_path =
            std::env::var("FOYA_TEST_SSH_BINARY").expect("FOYA_TEST_SSH_BINARY is required");
        let binary = fs::read(binary_path).expect("read integration kernel");
        let connection = SshConnection {
            name: "integration".into(),
            target,
            port,
            remote_port: 18787,
        };
        let state = remote_state(&connection).expect("probe integration host");
        let (url, probe) = deploy_binary_and_connect(
            &connection,
            "0123456789abcdef0123456789abcdef",
            state,
            &binary,
        )
        .expect("deploy integration kernel");
        assert!(url.starts_with("http://127.0.0.1:"));
        assert_eq!(probe.os, "Linux");
        {
            let mut guard = runtime().lock().expect("lock SSH runtime");
            let child = guard
                .as_mut()
                .and_then(|state| state.child.as_mut())
                .expect("running SSH tunnel");
            child.kill().expect("stop SSH tunnel");
            let _ = child.wait();
        }
        ensure_tunnel().expect("reconnect SSH tunnel");
        shutdown();
    }
}
