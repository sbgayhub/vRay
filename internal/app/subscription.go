package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sbgayhub/vray/internal/store"
	"github.com/sbgayhub/vray/internal/suburl"
)

// subscriptionTimeout 限制单次抓取。超过这个时间仍无响应的订阅，对站在
// 应用前的用户已经没有用处；无界抓取只会让导入按钮永远转圈。
const subscriptionTimeout = 30 * time.Second

// subscriptionMaxBytes 限制读取的响应大小。订阅最多几百 KB；超过这个量
// 要么不是订阅，要么是在试图耗尽内存。
const subscriptionMaxBytes = 8 << 20

// subscriptionUserAgent 是我们声明的客户端标识。部分面板对匿名客户端
// 返回不同内容或 403。
const subscriptionUserAgent = "vRay/1.0 (Xray client; Clash.Meta)"

// ImportInput 是导入框收到的内容：粘贴的分享链接、base64 内容，
// 或需要抓取的订阅 URL。
//
// URL 是另一种操作，而不是另一种语法：链接在本地解析，URL 则经由网络抓取。
type ImportInput struct {
	// Text 是粘贴的内容。
	Text string
	// Name 是文本为 URL 时的订阅名称；可选。
	Name string
}

// Import 导入粘贴的链接或订阅 URL。
//
// 它在后台运行：抓取订阅属于网络 IO，绝不能阻塞看到点击的那一帧。
func (s *Service) Import(in ImportInput) {
	started := false
	s.state.Update(func(sn *Snapshot) {
		if !sn.ImportBusy {
			sn.ImportBusy, sn.ImportAdded, sn.ImportProblem = true, 0, ""
			started = true
		}
	})
	if !started {
		return
	}
	go func() {
		defer s.state.Update(func(sn *Snapshot) { sn.ImportBusy = false })
		text := strings.TrimSpace(in.Text)
		if text == "" {
			s.setImport(0, "粘贴分享链接或订阅地址后再导入")
			return
		}
		if looksLikeSubscriptionURL(text) {
			s.importSubscription(text, strings.TrimSpace(in.Name))
			return
		}
		s.importText(text)
	}()
}

// looksLikeSubscriptionURL 报告 s 是 http(s) URL 还是粘贴的链接列表。
//
// 只允许 http(s)：订阅地址由我们抓取，放行其他协议会把导入框变成
// 让 vRay 代替用户打开文件或使用其他协议的工具。
func looksLikeSubscriptionURL(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || strings.ContainsAny(t, "\n\r") {
		// 多行就是链接列表，即使其中一行看起来像 URL。
		return false
	}
	return strings.HasPrefix(strings.ToLower(t), "http://") ||
		strings.HasPrefix(strings.ToLower(t), "https://")
}

// fetchSubscription 下载订阅内容及其流量响应头。
//
// 做成方法是为了让测试能指向 httptest 服务器；除非测试替换，生产路径
// 使用服务自己的客户端。
func (s *Service) fetchSubscription(ctx context.Context, rawURL string) (body []byte, info suburl.SubscriptionInfo, infoOK bool, err error) {
	return s.fetchSubscriptionWithAgent(ctx, rawURL, subscriptionUserAgent)
}

func (s *Service) fetchSubscriptionWithAgent(ctx context.Context, rawURL, agent string) (body []byte, info suburl.SubscriptionInfo, infoOK bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, info, false, fmt.Errorf("订阅地址无效：%w", err)
	}
	if agent == "" {
		agent = subscriptionUserAgent
	}
	req.Header.Set("User-Agent", agent)
	// 部分面板缺少它时会返回不同内容；这是约定俗成的提示。
	req.Header.Set("Accept", "*/*")

	client := s.httpClient

	if client == nil {
		client = &http.Client{Timeout: subscriptionTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, info, false, fmt.Errorf("无法连接订阅服务器：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, info, false, fmt.Errorf("订阅服务器返回 %s", resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, subscriptionMaxBytes+1))
	if err != nil {
		return nil, info, false, fmt.Errorf("读取订阅内容失败：%w", err)
	}
	if len(raw) > subscriptionMaxBytes {
		return nil, info, false, errors.New("订阅内容过大，可能不是订阅")
	}

	info, infoOK = suburl.ParseSubscriptionUserInfo(resp.Header.Get("Subscription-Userinfo"))
	return raw, info, infoOK, nil
}

// importSubscription 抓取订阅 URL、保存订阅并导入其节点。
func (s *Service) importSubscription(rawURL, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionTimeout)
	defer cancel()

	body, info, infoOK, err := s.fetchSubscription(ctx, rawURL)
	if err != nil {
		s.setImport(0, TrimDetail(err.Error()))
		return
	}

	res := suburl.ParseText(string(body))
	if len(res.Profiles) == 0 {
		msg := "订阅里没有可导入的节点"
		if len(res.Diagnostics) > 0 {
			msg = res.Diagnostics[0].Message
		}
		s.setImport(0, msg)
		return
	}

	if name == "" {
		name = subscriptionName(rawURL)
	}
	sourceID := newID()

	// 凭据写入（订阅地址与各节点）是 OS 凭据库 I/O，全部在服务锁之外
	// 完成；锁内只做一次整库读-改-写。逐节点写库会让数百节点的订阅
	// 导入阻塞连接与断开数秒。
	urlKey := "source/" + sourceID + "/url"
	if err := s.secrets.Set(urlKey, []byte(rawURL)); err != nil {
		s.setImport(0, "保存订阅地址失败："+TrimDetail(err.Error()))
		return
	}
	stored, policyNodes, err := s.prepareProfiles(res.Profiles, sourceID)
	if err != nil {
		// 订阅还没入库，回收已写入的地址，避免留下无主的机密。
		_ = s.secrets.Delete(urlKey)
		s.setImport(0, "保存节点失败："+TrimDetail(err.Error()))
		return
	}

	src := store.Source{
		Host:          subscriptionHost(rawURL),
		ID:            sourceID,
		Name:          name,
		URLRef:        urlKey,
		UserAgent:     subscriptionUserAgent,
		Enabled:       true,
		LastFetchedAt: time.Now(),
	}
	if infoOK {
		src.Upload = info.Upload
		src.Download = info.Download
		src.Total = info.Total
		if info.Expire > 0 {
			src.ExpireAt = time.Unix(info.Expire, 0)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.store.Load()
	if err == nil {
		d.Sources = append(d.Sources, src)
		d.Profiles = append(d.Profiles, stored...)
		if len(res.Groups) > 0 {
			d.Groups = mergePolicyGroups(d.Groups, res.Groups, sourceID, policyNodes)
		}
		err = s.store.Save(d)
	}
	if err != nil {
		s.setImport(0, "保存订阅失败："+TrimDetail(err.Error()))
		return
	}
	if err := s.Load(); err != nil {
		s.setImport(0, "读取配置失败："+TrimDetail(err.Error()))
		return
	}
	s.setImport(len(res.Profiles), "")
}

// subscriptionName 从订阅 URL 推导显示名称。
//
// 使用主机名与最后一段路径，这是用户能认出的部分：例如从
// https://panel.example.com/api/v1/client/subscribe 得到 "panel"。
// 绝不使用完整 URL，因为它携带令牌。
func subscriptionHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func subscriptionName(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return "订阅"
	}
	host := u.Hostname()
	// 去掉开头的 "www."，显示注册域名的第一段。
	host = strings.TrimPrefix(host, "www.")
	if i := strings.Index(host, "."); i > 0 {
		host = host[:i]
	}
	return host
}
