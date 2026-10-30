// sysproxy 包用于设置和清除操作系统的代理设置。
//
// 每个平台都使用各自的存储：Windows 将其保存在当前用户的 Internet Settings
// 注册表项中；macOS 使用 `networksetup`；Linux 使用桌面环境的 gsettings，
// 若无桌面环境则没有统一方案。各平台实现都会返回修改前的值，以便撤销修改；
// 调用方在应用修改前会将其记录到 effects 日志（internal/core/effects）。
//
// 此包不会自行执行任何操作：应用仅在用户明确操作时调用 Enable，并且仅在
// 补偿层中调用 Restore。
package sysproxy

// Settings 是应用期望使用的代理配置。
type Settings struct {
	// Host 和 Port 是本地混合入站代理。
	Host string
	Port int
	// Bypass 是以分号分隔的绕过列表。
	Bypass string
}

// Backend 表示单个平台的代理存储。
type Backend interface {
	// Enable 开启系统代理，并返回修改前的状态。
	//
	// 返回值是调用方写入 effects 日志的备份：如果没有它，崩溃后的恢复将无值可还原。
	Enable(Settings) (Previous, error)
	// Restore 将系统恢复到先前状态。
	Restore(Previous) error
	// Current 在不修改系统的情况下读取其代理状态。
	Current() (Previous, error)
}

// Previous 是 vRay 修改前的系统代理状态。
//
// 这些字段是各平台恢复时所需的通用信息：是否曾配置代理、代理地址以及绕过列表。
// 对于包含更多信息的平台（macOS 服务、Linux 配置项），额外细节保存在 Extra 中，
// 以便精确恢复。
type Previous struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address,omitempty"`
	Bypass  string `json:"bypass,omitempty"`
	// Extra 是以 JSON 保存的平台特定恢复信息。
	Extra string `json:"extra,omitempty"`
}

// BypassDefault 使本地流量绕过代理，与 Windows 自身的默认设置一致。
const BypassDefault = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;" +
	"172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;" +
	"172.28.*;172.29.*;172.30.*;172.31.*;192.168.*;<local>"
