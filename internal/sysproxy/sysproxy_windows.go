//go:build windows

package sysproxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// registryPath 是 WinINET（以及 Windows 自带的代理界面）保存当前用户设置的
// 位置。
//
// 特意使用每用户注册表配置单元：它无需管理员权限，也是“设置”应用编辑且恢复
// 时必须写回的位置。
const registryPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// 注册表值名称。
const (
	valProxyEnable = "ProxyEnable"
	valProxyServer = "ProxyServer"
	valProxyBypass = "ProxyOverride"
)

// wininet 是 Windows 平台的实现。
type wininet struct {
	// Key 是注册表键路径；可注入以便测试使用临时键，而不是真实键。
	Key string
}

// New 返回当前平台的后端。
func New() Backend { return &wininet{Key: registryPath} }

// read 从注册表读取代理状态。
func (w *wininet) read(name string) (string, bool, error) {
	key, err := openKey(w.Key)
	if err != nil {
		return "", false, err
	}
	defer closeKey(key)

	raw, typ, found, err := queryValue(key, name)
	if err != nil || !found {
		return "", false, err
	}
	switch typ {
	case regSZ:
		// 注册表字符串采用 UTF-16LE 编码，并以 NUL 结尾。
		return decodeUTF16(raw), true, nil
	case regDWORD:
		if len(raw) < 4 {
			return "", false, fmt.Errorf("系统代理：%s 是截断的 DWORD", name)
		}
		v := binary.LittleEndian.Uint32(raw)
		return strconv.FormatUint(uint64(v), 10), true, nil
	default:
		return "", false, fmt.Errorf("系统代理：%s 的类型 %d 不符合预期", name, typ)
	}
}

// queryValue 读取一个原始值及其类型。
func queryValue(key syscall.Handle, name string) ([]byte, uint32, bool, error) {
	var typ, size uint32
	// 首次调用用于获取所需大小。
	if err := regQueryValue(key, name, &typ, nil, &size); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	if size == 0 {
		return nil, typ, true, nil
	}
	buf := make([]byte, size)
	if err := regQueryValue(key, name, &typ, &buf[0], &size); err != nil {
		return nil, 0, false, err
	}
	return buf[:size], typ, true, nil
}

// decodeUTF16 将以 NUL 结尾的小端 UTF-16 缓冲区转换为字符串。
func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	// 删除终止符及其后的内容。
	for i, c := range u {
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}

// Current 实现 Backend。
func (w *wininet) Current() (Previous, error) {
	enabledRaw, _, err := w.read(valProxyEnable)
	if err != nil {
		return Previous{}, err
	}
	server, _, err := w.read(valProxyServer)
	if err != nil {
		return Previous{}, err
	}
	bypass, _, err := w.read(valProxyBypass)
	if err != nil {
		return Previous{}, err
	}
	return Previous{
		// ProxyEnable 是 DWORD；上述读取会将其表示为 "1"。
		Enabled: strings.TrimSpace(enabledRaw) == "1",
		Address: server,
		Bypass:  bypass,
	}, nil
}

// Enable 实现 Backend。
//
// 每个值都在单独调用中写入，并且先捕获先前值，因此中途失败时，返回的 Previous
// 仍能描述当前状态。调用方会在调用前将其写入日志；该层覆盖的是崩溃，而不是
// 普通失败。
func (w *wininet) Enable(s Settings) (Previous, error) {
	prev, err := w.Current()
	if err != nil {
		return Previous{}, err
	}

	bypass := s.Bypass
	if bypass == "" {
		bypass = BypassDefault
	}
	server := fmt.Sprintf("%s:%d", s.Host, s.Port)

	if err := w.writeString(valProxyServer, server); err != nil {
		return prev, err
	}
	if err := w.writeString(valProxyBypass, bypass); err != nil {
		return prev, err
	}
	if err := w.writeDWORD(valProxyEnable, 1); err != nil {
		return prev, err
	}
	refresh()
	return prev, nil
}

// Restore 实现 Backend。
func (w *wininet) Restore(p Previous) error {
	// 先恢复地址和绕过列表，再切换开关：代理已启用却指向空地址的短暂状态，
	// 比仍指向旧地址的短暂状态更糟。
	if p.Address != "" {
		if err := w.writeString(valProxyServer, p.Address); err != nil {
			return err
		}
	} else if err := w.deleteValue(valProxyServer); err != nil {
		return err
	}
	if p.Bypass != "" {
		if err := w.writeString(valProxyBypass, p.Bypass); err != nil {
			return err
		}
	} else if err := w.deleteValue(valProxyBypass); err != nil {
		return err
	}
	on := uint32(0)
	if p.Enabled {
		on = 1
	}
	if err := w.writeDWORD(valProxyEnable, on); err != nil {
		return err
	}
	refresh()
	return nil
}

func (w *wininet) writeString(name, value string) error {
	key, err := openKeyWrite(w.Key)
	if err != nil {
		return err
	}
	defer closeKey(key)
	return regSetString(key, name, value)
}

func (w *wininet) writeDWORD(name string, value uint32) error {
	key, err := openKeyWrite(w.Key)
	if err != nil {
		return err
	}
	defer closeKey(key)
	return regSetDWORD(key, name, value)
}

func (w *wininet) deleteValue(name string) error {
	key, err := openKeyWrite(w.Key)
	if err != nil {
		return err
	}
	defer closeKey(key)
	return regDeleteValue(key, name)
}

// 注册表底层调用。
//
// 这里直接调用 advapi32，而不通过 x/sys/windows：这些调用只有四个且稳定，
// 让整个客户端不必引入额外依赖，比节省几行代码更有价值。
var (
	advapi32        = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKey  = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryVal = advapi32.NewProc("RegQueryValueExW")
	procRegSetValue = advapi32.NewProc("RegSetValueExW")
	procRegDelete   = advapi32.NewProc("RegDeleteValueW")
	procRegCloseKey = advapi32.NewProc("RegCloseKey")
	wininet32       = syscall.NewLazyDLL("wininet.dll")
	procSetOption   = wininet32.NewProc("InternetSetOptionW")
)

const (
	hkeyCurrentUser = 0x80000001
	keyRead         = 0x20019
	keyWrite        = 0x20006
	regSZ           = 1
	regDWORD        = 4

	// InternetSetOption 标志会让 Windows 重新读取设置，从而使修改对已运行的程序
	// 也生效。
	optionSettingsChanged = 39
	optionRefresh         = 37
)

// errNotFound 是注册表中的 ERROR_FILE_NOT_FOUND。
var errNotFound = errors.New("系统代理：未找到注册表值")

func openKey(path string) (syscall.Handle, error) { return openKeyWith(path, keyRead) }

func openKeyWrite(path string) (syscall.Handle, error) { return openKeyWith(path, keyWrite) }

func openKeyWith(path string, access uint32) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	ret, _, _ := procRegOpenKey.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(p)),
		0,
		uintptr(access),
		uintptr(unsafe.Pointer(&h)),
	)
	if ret != 0 {
		return 0, fmt.Errorf("系统代理：打开注册表项 %s 失败：%w", path, syscall.Errno(ret))
	}
	return h, nil
}

func closeKey(h syscall.Handle) { procRegCloseKey.Call(uintptr(h)) }

func regQueryValue(h syscall.Handle, name string, typ *uint32, data *byte, size *uint32) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ret, _, _ := procRegQueryVal.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(n)),
		0,
		uintptr(unsafe.Pointer(typ)),
		uintptr(unsafe.Pointer(data)),
		uintptr(unsafe.Pointer(size)),
	)
	switch syscall.Errno(ret) {
	case 0:
		return nil
	case 2: // ERROR_FILE_NOT_FOUND：文件不存在
		return errNotFound
	default:
		return fmt.Errorf("系统代理：读取注册表值 %s 失败：%w", name, syscall.Errno(ret))
	}
}

func regSetString(h syscall.Handle, name, value string) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	// 存储的字符串以 NUL 结尾，其字节长度包含终止符。
	v, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	ret, _, _ := procRegSetValue.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(n)),
		0,
		regSZ,
		uintptr(unsafe.Pointer(&v[0])),
		uintptr(len(v)*2),
	)
	if ret != 0 {
		return fmt.Errorf("系统代理：设置注册表值 %s 失败：%w", name, syscall.Errno(ret))
	}
	return nil
}

func regSetDWORD(h syscall.Handle, name string, value uint32) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ret, _, _ := procRegSetValue.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(n)),
		0,
		regDWORD,
		uintptr(unsafe.Pointer(&value)),
		4,
	)
	if ret != 0 {
		return fmt.Errorf("系统代理：设置注册表值 %s 失败：%w", name, syscall.Errno(ret))
	}
	return nil
}

func regDeleteValue(h syscall.Handle, name string) error {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ret, _, _ := procRegDelete.Call(uintptr(h), uintptr(unsafe.Pointer(n)))
	switch syscall.Errno(ret) {
	case 0, 2: // 值已不存在，正符合预期
		return nil
	default:
		return fmt.Errorf("系统代理：删除注册表值 %s 失败：%w", name, syscall.Errno(ret))
	}
}

// refresh 通知正在运行的程序代理设置已更改。
func refresh() {
	procSetOption.Call(0, optionSettingsChanged, 0, 0)
	procSetOption.Call(0, optionRefresh, 0, 0)
}
