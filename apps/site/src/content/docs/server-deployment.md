---
title: 远程内核
description: 通过 SSH 自动部署，或使用 TLS 与 Bearer Token 运行 Foya 服务端。
slug: docs/server-deployment
---

Foya 远程内核面向单用户、单租户部署。一个访问令牌可以读取实例中的全部会话，
并可在 Agent 权限允许时操作服务器文件和执行命令。不要向互不信任的用户共享
同一个实例。

## 选择连接方式

| 场景 | 推荐方式 | 需要公开端口 |
|---|---|---|
| 家庭服务器、开发机 | SSH 自动部署 | 否 |
| 有域名的公网服务器 | HTTPS | 80/443 |
| 已有反向代理 | 内部明文 TCP + 代理 TLS | 仅代理端口 |

## SSH 自动部署

远端要求：

- Linux `x86_64` 或 `aarch64`；
- 本机可以通过 SSH Key 或 ssh-agent 免交互登录；
- 首次部署时，桌面端可以访问当前版本的 GitHub Release；
- SSH 服务启用 `AllowTcpForwarding yes`；
- 安装 `/usr/bin/bwrap`。

桌面端调用系统 OpenSSH，并固定使用 `BatchMode=yes`、
`StrictHostKeyChecking=accept-new` 和 10 秒连接超时。因此：

- 不支持在连接过程中交互输入密码或确认信息；
- 首次见到的主机密钥会按 TOFU（Trust On First Use）写入本机 `known_hosts`；
- 已记录主机的密钥发生变化时，OpenSSH 会拒绝连接。

首次部署前应通过可信渠道核对服务器 SSH 主机指纹，尤其不要在不可信网络中直接
接受一个从未连接过的 IP。

在桌面端打开“设置 → 内核”，添加 SSH 连接。可以选择本机
`~/.ssh/config` 中的 Host，也可以输入 `user@192.168.1.20`。

测试通过后点击“部署并连接”。桌面端会：

1. 探测远端操作系统、架构和 Bubblewrap；
2. 从同版本 GitHub Release 下载匹配架构的 gzip 内核和 SHA-256 文件；
3. 校验下载内容并写入桌面端的版本化缓存；
4. 解压并上传内核到 `~/.local/share/foya/bin/foya`；
5. 使用 `~/.local/share/foya/data` 保存数据；
6. 生成访问令牌，只在服务器保存 SHA-256 摘要；
7. 让内核监听 `127.0.0.1:8787`；
8. 建立本地 SSH 隧道，并在隧道退出后自动恢复；
9. 在桌面版本变化时按二进制摘要升级远端内核。

日志位于 `~/.local/share/foya/run/kernel.log`。退出桌面端会关闭隧道，但不会停止
远端内核。缓存存在后，后续部署不再依赖 GitHub；远端服务器始终不需要访问公网。

## HTTPS 服务

直接暴露 TCP 服务时，需要一个至少 32 字符的访问令牌和受客户端信任的 TLS 证书。

```bash
go build -trimpath -o foya ./cmd/foya
sudo install -m 0755 foya /usr/local/bin/foya
sudo useradd --system --create-home --home-dir /var/lib/foya foya
sudo install -d -o foya -g foya -m 0700 /var/lib/foya /srv/foya/workspaces
sudo install -d -o root -g foya -m 0750 /etc/foya

TOKEN="$(openssl rand -hex 32)"
printf 'Desktop access token: %s\n' "$TOKEN"
printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1 | \
  sudo tee /etc/foya/auth-token.sha256 >/dev/null
unset TOKEN
sudo chown root:foya /etc/foya/auth-token.sha256
sudo chmod 0640 /etc/foya/auth-token.sha256
```

安装证书和私钥后，可以直接启动：

```bash
foya serve \
  --listen 0.0.0.0:8787 \
  --data-dir /var/lib/foya \
  --auth-token-hash-file /etc/foya/auth-token.sha256 \
  --tls-cert /etc/foya/tls/fullchain.pem \
  --tls-key /etc/foya/tls/privkey.pem
```

### systemd

仓库的 `deploy/systemd` 目录提供 systemd 单元和环境文件示例：

```bash
sudo install -m 0644 deploy/systemd/foya.service /etc/systemd/system/foya.service
sudo cp deploy/systemd/foya.env.example /etc/foya/foya.env
sudo chmod 0640 /etc/foya/foya.env
sudo systemctl daemon-reload
sudo systemctl enable --now foya
```

服务默认监听 `0.0.0.0:8787`。防火墙只应允许预期客户端访问该端口。

## 反向代理

使用 Caddy、Nginx 或其他 TLS 终止代理时，Foya 可以在隔离网络中监听明文 TCP：

```bash
foya serve \
  --listen 127.0.0.1:8787 \
  --data-dir /var/lib/foya \
  --auth-token-hash-file /etc/foya/auth-token.sha256 \
  --allow-plaintext
```

`--allow-plaintext` 不应与公开监听地址组合。仓库的 `deploy/docker-compose.yml`
提供 Caddy 和 Foya 的容器部署示例。

### Docker 与 Caddy

```bash
mkdir -p deploy/secrets deploy/workspace
TOKEN="$(openssl rand -hex 32)"
printf 'Desktop access token: %s\n' "$TOKEN"
printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1 > \
  deploy/secrets/foya_auth_token.sha256
unset TOKEN
chmod 0600 deploy/secrets/foya_auth_token.sha256
sudo chown -R 10001:10001 deploy/workspace
printf 'FOYA_DOMAIN=foya.example.com\n' > deploy/.env
docker compose -f deploy/docker-compose.yml up -d --build
```

将域名 DNS 解析到服务器，并开放 TCP 80、TCP/UDP 443。不要单独发布
`kernel:8787`。Linux 主机必须允许非特权用户命名空间，以便容器中的 Bubblewrap
执行受限工具。

## 健康检查

`GET /healthz` 不要求凭证，其他 TCP 请求必须鉴权：

```bash
curl https://foya.example.com/healthz
curl -i https://foya.example.com/readyz
curl -H "Authorization: Bearer $FOYA_TOKEN" \
  https://foya.example.com/readyz
```

预期结果依次为 `200`、`401` 和 `200`。

## 桌面端 HTTPS 连接

在“设置 → 内核 → HTTPS”中填写服务地址和原始访问令牌。桌面端使用系统证书
信任库，不接受未受信任的自签名证书。

原始令牌只保存在桌面配置文件 `kernel-connection.json` 中；服务器只保存摘要。
远程模式不会启动本地 Sidecar。

## 配置

服务参数均有对应环境变量：

| 参数 | 环境变量 | 说明 |
|---|---|---|
| `--listen` | `FOYA_LISTEN_ADDR` | TCP 监听地址；未设置时使用 Unix Socket |
| `--socket` | `FOYA_SOCKET_PATH` | Unix Socket 路径 |
| `--data-dir` | `FOYA_DATA_DIR` | 持久数据目录 |
| `--auth-token-hash-file` | `FOYA_AUTH_TOKEN_HASH_FILE` | Token 摘要文件 |
| 无 | `FOYA_AUTH_TOKEN_SHA256` | 直接提供 SHA-256 Token 摘要 |
| `--tls-cert` | `FOYA_TLS_CERT_FILE` | PEM 证书链 |
| `--tls-key` | `FOYA_TLS_KEY_FILE` | PEM 私钥 |
| `--allow-plaintext` | `FOYA_ALLOW_PLAINTEXT` | 仅供可信反向代理网络 |

## 运行约束

- 每个数据目录只允许一个 Foya 进程。
- 不支持多副本、水平扩容或多租户权限隔离。
- 项目路径是服务器路径。
- 手动审批和浏览器操作需要至少一个桌面客户端保持连接。
- SQLite、Artifact、插件和配置都位于数据目录，备份时应整体处理。
- 数据目录和桌面连接配置都可能包含凭证，必须按密钥材料保护。

## 运维

- 存活探针：`GET /healthz`；
- 带认证的就绪探针：`GET /readyz`；
- 日志：标准输出和标准错误，由 systemd 或容器运行时采集；
- 指标与链路：使用 `FOYA_OTEL_*` 和标准 `OTEL_EXPORTER_*` 环境变量；
- 备份：停止服务后备份整个数据目录和项目目录；
- Token 轮换：替换摘要文件并重启服务，再更新桌面连接；
- 升级：替换二进制或重建镜像后重启；单实例约束下会有短暂中断。
