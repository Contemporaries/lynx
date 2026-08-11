# 日志

[English](logging.md) | **中文**

Lynx 以**网络流量**为主输出进程日志（谁连了什么、走哪条链路、字节数），并附带运维事件。级别：`debug` / `info`（默认） / `warn` / `error`。设置某级时输出该级及更严重。

## 配置

```json
"log": { "level": "info" }
```

命令行覆盖：`-log-level debug`。

## `info` 下可见内容

```text
flow open / flow close（TCP，含 path、via、up/down、duration）
udp associate open / close（包数与字节）
auto: path changed …
```

`debug` 可见逐包 UDP 目标等细节；`warn` 覆盖私网拒绝、限流、未授权、WSS→direct 降级。

实时流：管理 API `GET /api/v1/logs?level=info`（SSE）。见 [mgmt-api.zh-CN.md](mgmt-api.zh-CN.md)。
