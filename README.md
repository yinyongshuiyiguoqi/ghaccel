# ghaccel

GitHub 专用开源加速器：智能 DNS + hosts，不改网络栈、不装证书、不接管代理。

仓库：<https://github.com/yinyongshuiyiguoqi/ghaccel>

它解决的是「GitHub 解析慢、解析到不可达 IP、raw / codeload / objects 时快时慢」这一类问题，
而不是「IP 被完全封锁」——后者 hosts 无能为力，README 末尾会写清楚边界。

```text
DoH（阿里 / DNSPod） ─┐
内置种子 IP ──────────┼─> 候选 IP ─> TCP + TLS(SNI) + HTTP HEAD 探测 ─> 打分选优
上一次选中的 IP ──────┘                                                   │
                                                                          ▼
                                              hosts 标记块（原子替换、可回滚、可选刷新 DNS）
```

## 特性

- **只改 hosts**：写在一个可识别的标记块里，块外内容一个字节都不动。
- **不信任 DNS**：每个候选 IP 都必须通过对应域名的 TLS 证书校验，TCP 通但 TLS 失败会被淘汰。
- **DoH 优先**：默认使用阿里与 DNSPod（Cloudflare / Google 在部分网络下不可达，默认关闭）。
- **有兜底**：DoH 全挂时退到内置种子 IP 和上一轮结果，不会把 hosts 写坏。
- **可回滚**：首次修改前保存基线备份，`restore` 后恢复原文件。
- **常驻刷新**：`start` 后每 15 分钟重新选路，映射不变就不写盘。
- **纯标准库**：无第三方 Go 依赖，单文件二进制。

## 安装

### 下载现成程序（推荐）

Releases 提供 6 个平台的压缩包，以及 `sha256sums.txt`：

| 平台 | 文件 |
|---|---|
| Windows x64 | `ghaccel_0.1.0_windows_amd64.zip` |
| Windows ARM64 | `ghaccel_0.1.0_windows_arm64.zip` |
| Linux x64 / ARM64 | `ghaccel_0.1.0_linux_*.tar.gz` |
| macOS Intel / Apple Silicon | `ghaccel_0.1.0_darwin_*.tar.gz` |

Windows 用法：

```powershell
# 解压后先校验
Get-FileHash .\ghaccel_0.1.0_windows_amd64.zip -Algorithm SHA256
# 与 sha256sums.txt 中对应行比对

.\ghaccel.exe doctor
.\ghaccel.exe once --dry-run
# 管理员终端
.\ghaccel.exe once
```

程序带图标和文件版本信息。它是命令行工具，双击只会闪一下窗口，请从终端运行；日常使用建议放到固定目录并加入 PATH。

### 用 Go 安装

需要 Go 1.27+：

```bash
go install github.com/yinyongshuiyiguoqi/ghaccel/cmd/ghaccel@latest
```

### 自行构建

```bash
git clone https://github.com/yinyongshuiyiguoqi/ghaccel.git
cd ghaccel
go build -trimpath -ldflags "-s -w" -o bin/ghaccel ./cmd/ghaccel
```

打包全部平台：

```powershell
.\scripts\build-release.ps1 -Version 0.1.0
```

产物在 `dist/`，包含压缩包和校验和。

## 快速开始

```bash
ghaccel init                 # 写入默认配置
ghaccel doctor               # 检查 DoH、TLS、hosts 权限
ghaccel once --dry-run -v    # 只探测、不写文件，先看效果

# 用管理员终端执行：
ghaccel once                 # 选路并写入 hosts
ghaccel start                # 后台每 15 分钟刷新
ghaccel status               # 查看守护进程与最近一轮
ghaccel stop                 # 停止刷新（hosts 保留）
ghaccel restore              # 移除 hosts 中的托管块
```

Windows 上写 `C:\Windows\System32\drivers\etc\hosts` 需要管理员权限。
`once` 会在联网前检查权限，权限不足时直接以退出码 3 结束，并打印提权命令。

## 命令

| 命令 | 作用 |
|---|---|
| `init [--force] [--print]` | 生成配置 |
| `once [--dry-run] [--json] [--domains a,b] [--timeout 60s]` | 跑一轮 |
| `start [--foreground] [--interval 15m]` | 启动后台刷新 |
| `stop [--restore]` | 停止后台刷新 |
| `status [--json]` | 状态与最近一轮结果 |
| `restore [--dry-run]` | 移除托管块 |
| `doctor [--json] [--strict]` | 环境与网络诊断 |
| `version` | 版本信息 |

退出码：

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 运行失败 |
| 2 | 用法或配置错误 |
| 3 | 需要管理员 |
| 4 | 降级：部分核心域名没有可用 IP |
| 5 | hosts 标记块损坏或冲突 |

## 配置

默认路径 `%APPDATA%\ghaccel\config.json`（Linux/macOS 为 `~/.config/ghaccel/config.json`）。
`ghaccel init` 会生成完整配置，常用项：

```jsonc
{
  "doh": {
    "enabled": true,
    "providers": [
      { "name": "ali",    "url": "https://dns.alidns.com/resolve", "enabled": true,  "priority": 1, "timeout_ms": 2500 },
      { "name": "dnspod", "url": "https://doh.pub/dns-query",       "enabled": true,  "priority": 2, "timeout_ms": 2500 },
      { "name": "cloudflare", "url": "https://cloudflare-dns.com/dns-query", "enabled": false },
      { "name": "google",     "url": "https://dns.google/resolve",           "enabled": false }
    ]
  },
  "probe": {
    "attempts": 2,
    "max_candidates_per_domain": 6,
    "concurrency": 16,
    "tcp_timeout_ms": 1500,
    "tls_timeout_ms": 2500,
    "attempt_timeout_ms": 4000,
    "http_head": true
  },
  "selection": { "min_improvement_ms": 150, "on_no_candidate": "keep" },
  "refresh": { "interval_minutes": 15 },
  "hosts": { "flush_dns": true, "conflict_policy": "skip" }
}
```

- `domains: []` 使用内置域名目录；填写后**替换**目录。
- `extra_domains` 追加精确域名（hosts 不支持通配符）。
- `exclude_domains` 排除域名。
- `conflict_policy: "skip"`：如果 hosts 里已有你自己写的同域条目，就跳过该域名，绝不覆盖。

## 内置域名

核心（缺失即退出码 4）：

`github.com`、`api.github.com`、`raw.githubusercontent.com`、`codeload.github.com`、
`objects.githubusercontent.com`、`gist.github.com`、`gist.githubusercontent.com`、
`github.githubassets.com`

可选：`media/avatars*/camo/cloud/user-images/private-user-images/desktop/favicons.githubusercontent.com`、
`github.io`、`githubstatus.com`、`github.blog`、`collector/alive/central/education/live.github.com`、
`github.community`、`pipelines.actions.githubusercontent.com`、`npm.pkg.github.com`、
`maven.pkg.github.com`、`ghcr.io`、`github.global.ssl.fastly.net`。

## 边界（请先读）

- **hosts 没有通配符**：`*.github.io` 无法自动覆盖。需要时把完整域名加到 `extra_domains`。
- **浏览器 Secure DNS 会绕过 hosts**：Chrome/Edge 开启“安全 DNS”时可能仍然走自己的 DoH，
  需要在浏览器设置里关闭，或使用系统级策略。
- **IPv6 未覆盖**：默认只写 IPv4；应用若优先使用 AAAA，hosts 不会生效。
- **不解决 IP 封锁**：如果目标 IP 在链路层被阻断，换 IP 也无效。
- **不加速全部流量**：只处理域名目录内的 GitHub 域，其他网站不受影响。
- **写 hosts 需要管理员**，且可能被杀软/受控文件夹访问拦截；错误会明确报出来。

## 安全设计

- 不生成、不安装根证书；不做 HTTPS 中间人。
- 不修改系统代理、PAC、路由表；不加载驱动。
- hosts 写入是「标记块 + 临时文件 + rename + 回读校验」，失败不会留下半截文件。
- 首次修改前保存 `hosts.before-ghaccel` 基线，另有滚动备份。
- 守护进程只监听 `127.0.0.1` 的随机端口，停止接口需要随机 token。
- 无遥测、无上报。

## 开发

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

目录：

```text
cmd/ghaccel           入口
internal/app          命令与输出
internal/config       JSON 配置
internal/domains      域名目录与种子
internal/resolver     DoH、候选合并、熔断
internal/probe        TCP/TLS/HTTP 探测与打分
internal/hosts        hosts 标记块、原子写入、备份、刷新 DNS
internal/daemon       守护进程信息、控制端口、后台启动
internal/state        最近一轮状态
```

## FAQ

**Q: 为什么 DoH 默认只有阿里和 DNSPod？**
A: 实测 Cloudflare / Google 在部分网络下超时；保留配置项，需要时可打开。

**Q: 为什么 `once` 退出码是 4？**
A: 至少一个核心域名没有通过 TLS 校验的候选 IP。看 `status` 或 `--json` 里的 `error` 字段。

**Q: 改了 hosts 但浏览器没变化？**
A: 先 `ipconfig /flushdns`，再检查浏览器 Secure DNS，最后重启浏览器（QUIC/Alt-Svc 缓存）。

**Q: 怎么完全卸载？**
A: `ghaccel stop --restore`，然后删除 `%APPDATA%\ghaccel` 与 `%LOCALAPPDATA%\ghaccel`。

## License

MIT
