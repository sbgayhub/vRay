package resources

import _ "embed"

// Icon 也供打包工具使用。将其嵌入后，独立可执行文件无需相邻的 resources 目录，
// 也能使用相同的窗口/任务栏图标。
//
//go:embed icon.png
var Icon []byte
