# Changelog

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
