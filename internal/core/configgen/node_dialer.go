package configgen

import "encoding/json"

// BuildNodeDialer 复用节点完整的协议/传输设置，但不打开本地监听，
// 也不添加路由规则或直连回退。
func BuildNodeDialer(node Outbound) ([]byte, error) {
	in := Input{Active: node, Mode: ModeSystemProxy, Settings: Settings{MixedPort: 1}}
	for _, diagnostic := range validate(in) {
		if diagnostic.blocks() {
			return nil, ErrInvalid
		}
	}
	return json.Marshal(struct {
		Outbounds []outbound `json:"outbounds"`
	}{Outbounds: []outbound{buildProxy("", node)}})
}
