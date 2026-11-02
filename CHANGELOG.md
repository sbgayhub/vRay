# Changelog

## [0.2.4] - 2026-11-02

### 修复

- 规则的保存、删除、移动与启停改为在同一把锁内完成读-改-写，快速连点不再丢失操作，也不会把已删除的规则写回。
- 重复点击连接或断开会被忽略，不再把正常运行中的隧道报告为“内核启动失败”。
- 加密密钥的生成移入互斥锁并缓存，并发写入多个较大凭据时不再导致先写入的值永久无法解密；系统凭据库不可用时回退加密会在日志中明确提示。
- 只配置进程名的路由规则不再被静默丢弃，此前这类规则永远不会生效。
- 日志文件轮转失败时不再每行重试、也不再向已关闭的文件句柄写入，文件日志不会静默中断。
- 策略组节点取证失败时不再静默把节点剔出组外：部分失败记录警告日志，全部失败时连接会报错并说明原因。
- 流量采样不再持有采样器锁调用界面回调，消除潜在死锁。

### 优化

- 日志页按缓冲区变更签名缓存显示内容，空闲帧不再复制整个缓冲区并逐行做描述匹配。
- 标题栏、主题配色、强调色文字对比与策略组索引按帧缓存，重绘时不再重复做深拷贝与颜色计算，界面在悬停动画下更省 CPU。
- 翻译结果按语言与原文记忆化，未收录的字符串不再每帧重复正则匹配。
- 策略组流量计数改用单次批量查询，节点较多时每秒的 API 往返次数从数十次降到个位数。
- 导入订阅时凭据写入移出操作锁，节点一次批量入库；数百节点的订阅导入不再长时间阻塞连接与断开。
- 数据存储按修改时间校验缓存已读取的文件内容，减少重复读盘。
- 订阅抓取复用 HTTP 连接池，减少重复的 TLS 握手。
- 加载日志缓冲、脱敏地址匹配等小热点不再重复分配。

### 其他

- 提交了此前未入库的 `tools/` 与 `cmd/` 工具，`gbkgen` 补充 Windows 构建约束，CI 增加 mygo 版本门禁。
- 移除代码注释中对设计文档路径的引用。

## [0.2.3] - 2026-10-31

### 新增

-  新增并行延迟测试功能及设置

## [0.2.2] - 2026-10-31

### 新增

- 添加readme
- 修复actions流程

## [0.2.1] - 2026-10-30

### Added

- Custom policy groups with editable names, four policy types, ordered members, nested references and validation for cycles and invalid members.
- Exit-IP and region filtering with bulk selection for building groups from nodes that share a persisted exit.
- A title-bar update action that downloads and stages an available signed release, then becomes a restart action.
- Platform-standard per-user data storage with automatic legacy migration, `VRAY_DATA` overrides and portable mode.
- A data-directory setting with conflict checks, restart-based migration, preservation of the old directory, backup of stale default-directory data and one-click restore to the platform default.
- Redesigned application and tray icons, with state-aware artwork for idle, connecting, connected and failed states.

### Changed

- The settings page now keeps its title and Save/Cancel actions fixed while only the settings body scrolls.
- Custom groups use their own stable namespace, so subscription refreshes do not replace or delete them.
- Process logging now uses `log/slog` with a Chinese console formatter.
- Source comments and runtime diagnostics are written in Chinese; duplicated helpers and dead code have been removed.

## [0.2.0] - 2026-10-30

### Added

- Signed application updates with a daily 04:00 check, manual checks, install progress and restart.
- Software update, About and Disclaimer & Terms of Use entries in Settings.
- A versioned first-run agreement that blocks all network activity until accepted.
- GitHub Actions quality checks and tag-triggered Windows, macOS and Linux release builds.

### Changed

- Updates use MyGO's Ed25519-verified update manifests and publish through a draft GitHub Release.
- Legal acceptance, automatic-update preference and update state are integrated with the local settings store.

## [0.1.1] - 2026-10-30

### Changed

- Refined the home dashboard, policy groups, subscriptions, node details and window materials.
- Added persistent exit-IP enrichment and download speed tests.
