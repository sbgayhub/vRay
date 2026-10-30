package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// IPDetails 描述经由该节点请求时观察到的公网出口。
type IPDetails struct {
	Address                                               string
	request                                               uint64
	revision                                              time.Time
	IP, Country, Region, City, ASN, Provider, Type, Score string
	Loading                                               bool
	Error                                                 string
	CheckedAt                                             time.Time
}

type nodeClientFactory func(context.Context, configgen.Outbound) (*http.Client, func(), error)

// InspectSelectedNode 只查询尚无成功观测的节点。已保存的结果不会过期；
// InspectNode 才是显式刷新操作。假服务抑制自动网络调用；显式查询测试
// 使用注入了 HTTP 端点的 InspectNode。
func (s *Service) InspectSelectedNode(id string) <-chan struct{} {
	closed := func() <-chan struct{} { done := make(chan struct{}); close(done); return done }
	if s.noResolve {
		return closed()
	}
	snap := s.state.Snapshot()
	var selected ProfileView
	for _, profile := range snap.Profiles {
		if profile.ID == id {
			selected = profile
			break
		}
	}
	info := snap.NodeDetails[id]
	if selected.ID == "" || (info.Loading && info.revision.Equal(selected.revision)) {
		return closed()
	}
	// 绝不在节点之间共享出口：同一个中继地址可能因端口、凭据和传输方式
	// 不同而拥有完全不同的出口。
	s.cancelNodeLookup()
	if info.IP != "" {
		return closed()
	}
	return s.InspectNode(id)
}

func (s *Service) cancelNodeLookup() {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	if s.inspectCancel != nil {
		s.inspectCancel()
		s.inspectCancel = nil
	}
}

func (s *Service) CancelInspection() {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	if s.inspectCancel != nil {
		s.inspectCancel()
		s.inspectCancel = nil
	}
	if s.dnsCancel != nil {
		s.dnsCancel()
		s.dnsCancel = nil
	}
	if s.downloadCancel != nil {
		s.downloadCancel()
		s.downloadCancel = nil
	}
}

// InspectNode 请求外部服务观察经由选中节点到达的请求。同时只运行一个
// 查询；新查询会取消旧查询。
func (s *Service) InspectNode(id string) <-chan struct{} {
	done := make(chan struct{})
	var selected ProfileView
	for _, p := range s.state.Snapshot().Profiles {
		if p.ID == id {
			selected = p
			break
		}
	}
	if selected.ID == "" {
		close(done)
		return done
	}
	s.cancelNodeLookup()
	s.viewMu.RLock()
	limited := time.Now().Before(s.ipRetryAt)
	s.viewMu.RUnlock()
	if limited {
		s.state.Update(func(sn *Snapshot) {
			if sn.NodeDetails == nil {
				sn.NodeDetails = make(map[string]IPDetails)
			}
			info := sn.NodeDetails[id]
			info.Address, info.revision, info.Loading, info.request = selected.Address, selected.revision, false, 0
			info.Error = "IP 数据源达到查询限额，稍后再试"
			sn.NodeDetails[id] = info
		})
		close(done)
		return done
	}
	s.viewMu.Lock()
	if s.inspectCancel != nil {
		s.inspectCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.inspectCancel = cancel
	s.inspectGeneration++
	generation := s.inspectGeneration
	s.viewMu.Unlock()
	s.state.Update(func(sn *Snapshot) {
		if sn.NodeDetails == nil {
			sn.NodeDetails = make(map[string]IPDetails)
		}
		info := sn.NodeDetails[id]
		info.Address, info.revision, info.request = selected.Address, selected.revision, generation
		info.Loading, info.Error = true, ""
		sn.NodeDetails[id] = info
	})
	go func() {
		defer close(done)
		defer cancel()
		info, err := s.lookupIP(ctx, selected)
		if ctx.Err() != nil {
			info = IPDetails{Address: selected.Address, revision: selected.revision}
			if ctx.Err() == context.DeadlineExceeded {
				err = fmt.Errorf("出口 IP 查询超时，请检查节点后重试")
			} else {
				err = fmt.Errorf("查询已取消")
			}
		}
		if err == nil {
			info.CheckedAt = time.Now().UTC()
			err = s.persistNodeDetails(ctx, selected, generation, info)
		}
		s.state.Update(func(sn *Snapshot) {
			if sn.NodeDetails[id].request != generation {
				return
			}
			if err != nil {
				previous := sn.NodeDetails[id]
				previous.Loading, previous.Error = false, err.Error()
				sn.NodeDetails[id] = previous
			} else {
				info.request = generation
				sn.NodeDetails[id] = info
			}
		})
	}()
	return done
}

// 与订阅/设置/流量写入串行化。提交前同时校验请求令牌与已存储节点，
// 防止迟到响应覆盖更新的请求，或复活已删除/已变更的节点。
func (s *Service) persistNodeDetails(ctx context.Context, selected ProfileView, generation uint64, info IPDetails) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("查询已取消，未更新本地记录")
	}
	if s.state.Snapshot().NodeDetails[selected.ID].request != generation {
		return fmt.Errorf("查询已被替换，未更新本地记录")
	}
	d, err := s.store.Load()
	if err != nil {
		return fmt.Errorf("无法读取本地出口信息，原记录未更改")
	}
	matched := false
	for _, profile := range d.Profiles {
		if profile.ID == selected.ID && profile.UpdatedAt.Equal(selected.revision) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("节点已变更或删除，未更新本地记录")
	}
	if d.NodeDetails == nil {
		d.NodeDetails = make(map[string]store.NodeIPInfo)
	}
	d.NodeDetails[selected.ID] = info.stored()
	if err := s.store.Save(d); err != nil {
		return fmt.Errorf("出口信息保存失败，原记录未更改")
	}
	return nil
}

func (info IPDetails) stored() store.NodeIPInfo {
	return store.NodeIPInfo{
		IP: info.IP, Country: info.Country, Region: info.Region, City: info.City,
		ASN: info.ASN, Provider: info.Provider, Type: info.Type, Score: info.Score, CheckedAt: info.CheckedAt,
	}
}

func ipDetailsFromStore(info store.NodeIPInfo) IPDetails {
	return IPDetails{
		IP: info.IP, Country: info.Country, Region: info.Region, City: info.City,
		ASN: info.ASN, Provider: info.Provider, Type: info.Type, Score: info.Score, CheckedAt: info.CheckedAt,
	}
}

func (s *Service) lookupIP(ctx context.Context, selected ProfileView) (IPDetails, error) {
	info := IPDetails{Address: selected.Address, revision: selected.revision}
	key := os.Getenv("VRAY_IPAPI_KEY")
	if s.state.Snapshot().Settings.LookupKeyConfigured {
		stored, err := s.secrets.Get("preferences/ipapi-key")
		if err != nil {
			return info, fmt.Errorf("无法读取 IP 查询密钥，请重新配置")
		}
		key = string(stored)
	}
	parameters := map[string]string{}
	if key != "" {
		parameters["key"] = key
	}
	// 省略 q 表示询问服务商看到的来源，而不是服务器的 DNS/入口地址。
	// 下面的 HTTP 传输被固定到该节点。
	body, _ := json.Marshal(parameters)
	endpoint := s.ipEndpoint
	if endpoint == "" {
		endpoint = "https://api.ipapi.is/"
	}
	primaryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(primaryCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return info, fmt.Errorf("无法创建 IP 查询")
	}
	req.Header.Set("Content-Type", "application/json")
	client, cleanup, err := s.clientForNode(ctx, selected)
	if err != nil {
		return info, fmt.Errorf("无法建立节点查询通道，请检查节点配置")
	}
	defer cleanup()
	result, err := s.fetchIPDetails(client, req, info)
	if err == nil || ctx.Err() != nil || s.ipEndpoint != "" {
		return result, err
	}
	s.viewMu.RLock()
	limited := time.Now().Before(s.ipRetryAt)
	s.viewMu.RUnlock()
	if limited {
		return result, err
	}
	// 某些出口无法访问 ipapi.is。通过同一强制节点传输查询另一个服务商；
	// 绝不能拿本机 IP 冒充。
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/", nil)
	if err != nil {
		return info, fmt.Errorf("无法创建备用 IP 查询")
	}
	return s.fetchIPDetails(client, req, info)
}

func (s *Service) fetchIPDetails(client *http.Client, req *http.Request, info IPDetails) (IPDetails, error) {
	response, err := client.Do(req)
	if err != nil {
		return info, fmt.Errorf("无法通过此节点查询出口 IP，请检查节点连接后重试")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden {
		delay := 5 * time.Minute
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			delay = time.Duration(min(seconds, 86400)) * time.Second
		}
		s.viewMu.Lock()
		s.ipRetryAt = time.Now().Add(delay)
		s.viewMu.Unlock()
	}
	if response.StatusCode != http.StatusOK {
		return info, fmt.Errorf("IP 数据源返回 %d，请稍后重试", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	if err != nil {
		return info, fmt.Errorf("读取 IP 信息失败")
	}
	return decodeIPDetails(raw, info)
}

func decodeIPDetails(raw []byte, info IPDetails) (IPDetails, error) {
	var payload struct {
		IP, Error, Country, Region, City string
		Success                          *bool
		Company, ASN                     json.RawMessage
		Location                         struct{ Country, State, City string }
		Connection                       struct {
			ASN      int
			Org, ISP string
		}
		IsDatacenter *bool `json:"is_datacenter"`
		IsMobile     *bool `json:"is_mobile"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return info, fmt.Errorf("无法解析 IP 信息")
	}
	if payload.Error != "" || (payload.Success != nil && !*payload.Success) {
		return info, fmt.Errorf("IP 数据源没有返回可用信息")
	}
	ip, err := netip.ParseAddr(payload.IP)
	if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return info, fmt.Errorf("数据源没有返回有效的公网出口 IP")
	}
	info.IP = ip.Unmap().String()
	info.Country, info.Region, info.City = payload.Country, payload.Region, payload.City
	if payload.Location.Country != "" {
		info.Country, info.Region, info.City = payload.Location.Country, payload.Location.State, payload.Location.City
	}
	var company struct {
		Name, Type string
		Score      string `json:"abuser_score"`
	}
	if json.Unmarshal(payload.Company, &company) == nil {
		info.Provider, info.Type, info.Score = company.Name, company.Type, company.Score
	} else {
		_ = json.Unmarshal(payload.Company, &info.Provider)
	}
	var asn struct {
		ASN       int
		Org, Type string
	}
	if json.Unmarshal(payload.ASN, &asn) == nil && asn.ASN > 0 {
		info.ASN = fmt.Sprintf("AS%d · %s", asn.ASN, asn.Org)
		if info.Type == "" {
			info.Type = asn.Type
		}
	} else {
		_ = json.Unmarshal(payload.ASN, &info.ASN)
	}
	if payload.Connection.ASN > 0 {
		info.ASN = fmt.Sprintf("AS%d · %s", payload.Connection.ASN, payload.Connection.Org)
		info.Provider = payload.Connection.ISP
		if info.Provider == "" {
			info.Provider = payload.Connection.Org
		}
	}
	if payload.IsDatacenter != nil && *payload.IsDatacenter {
		info.Type = "数据中心"
	}
	if payload.IsMobile != nil && *payload.IsMobile {
		info.Type = "移动网络"
	}
	info.Provider, info.ASN = strings.TrimSpace(info.Provider), strings.TrimSpace(info.ASN)
	return info, nil
}
