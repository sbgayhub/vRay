package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

const (
	DownloadTestMaxBytes int64 = 32 << 20
	downloadTestURL            = "https://speed.cloudflare.com/__down"
)

type DownloadTest struct {
	store.DownloadSpeed
	Running                             bool
	Error                               string
	CurrentBytes, CurrentBytesPerSecond int64
	request                             uint64
	revision                            time.Time
}

type downloadLimits struct {
	MaxBytes          int64
	Duration, Timeout time.Duration
}

func (s *Service) CancelDownload() {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	if s.downloadCancel != nil {
		s.downloadCancel()
		s.downloadCancel = nil
	}
}

// TestDownload 只在用户显式操作时运行。它从不选中或重连节点，
// 测量期间保留上一次成功结果。
func (s *Service) TestDownload(id string) <-chan struct{} {
	done := make(chan struct{})
	var selected ProfileView
	for _, profile := range s.state.Snapshot().Profiles {
		if profile.ID == id {
			selected = profile
			break
		}
	}
	if selected.ID == "" {
		close(done)
		return done
	}
	limits := s.downloadLimits
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = DownloadTestMaxBytes
	}
	if limits.Duration <= 0 {
		limits.Duration = 8 * time.Second
	}
	if limits.Timeout <= 0 {
		limits.Timeout = 25 * time.Second
	}
	s.viewMu.Lock()
	if s.downloadCancel != nil {
		s.downloadCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), limits.Timeout)
	s.downloadCancel = cancel
	s.downloadGeneration++
	generation := s.downloadGeneration
	s.viewMu.Unlock()
	s.state.Update(func(sn *Snapshot) {
		if sn.NodeSpeeds == nil {
			sn.NodeSpeeds = make(map[string]DownloadTest)
		}
		result := sn.NodeSpeeds[id]
		result.Running, result.Error, result.request, result.revision = true, "", generation, selected.revision
		result.CurrentBytes, result.CurrentBytesPerSecond = 0, 0
		sn.NodeSpeeds[id] = result
	})
	go func() {
		defer close(done)
		defer cancel()
		result, err := s.measureNodeDownload(ctx, selected, limits, func(bytes int64, elapsed time.Duration) {
			s.state.Update(func(sn *Snapshot) {
				current := sn.NodeSpeeds[id]
				if current.request != generation {
					return
				}
				current.CurrentBytes, current.CurrentBytesPerSecond = bytes, downloadRate(bytes, elapsed)
				sn.NodeSpeeds[id] = current
			})
		})
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = fmt.Errorf("下载测速超时，请检查节点后重试")
			} else {
				err = fmt.Errorf("下载测速已取消")
			}
		}
		// 专用出站没有主代理计数器。把它收到的数据量计入一次，
		// 取消或失败的尝试也要计入。
		s.recordDownloadBytes(id, result.Bytes)
		if err == nil {
			result.CheckedAt = time.Now().UTC()
			err = s.persistDownload(ctx, selected, generation, result)
		} else if result.Bytes > 0 {
			s.mu.Lock()
			_ = s.flushTrafficLocked()
			s.mu.Unlock()
		}
		s.state.Update(func(sn *Snapshot) {
			current := sn.NodeSpeeds[id]
			if current.request != generation {
				return
			}
			current.Running = false
			current.CurrentBytes, current.CurrentBytesPerSecond = 0, 0
			if err != nil {
				current.Error = err.Error()
			} else {
				current.DownloadSpeed, current.Error = result, ""
			}
			sn.NodeSpeeds[id] = current
		})
	}()
	return done
}

func (s *Service) measureNodeDownload(ctx context.Context, profile ProfileView, limits downloadLimits, progress func(int64, time.Duration)) (store.DownloadSpeed, error) {
	endpoint := s.downloadEndpoint
	if endpoint == "" {
		endpoint = downloadTestURL
	}
	target, err := url.Parse(endpoint)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
		return store.DownloadSpeed{}, fmt.Errorf("下载测速地址无效")
	}
	query := target.Query()
	query.Set("bytes", strconv.FormatInt(limits.MaxBytes, 10))
	target.RawQuery = query.Encode()
	result := store.DownloadSpeed{Endpoint: target.String()}
	client, cleanup, err := s.clientForNode(ctx, profile)
	if err != nil {
		return result, err
	}
	defer cleanup()
	// 父上下文涵盖建立连接与有界下载两个阶段。
	// 不要改动共享/测试客户端的超时或 IP 查询超时。
	copyClient := *client
	copyClient.Timeout = 0
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	requestContext, cancel := context.WithCancel(ctx)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target.String(), nil)
	if err != nil {
		return result, fmt.Errorf("无法创建下载测速请求")
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := copyClient.Do(request)
	if err != nil {
		return result, fmt.Errorf("无法通过此节点下载测速文件")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("测速源返回 %d，请稍后重试", response.StatusCode)
	}
	kind := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(kind, "text/html") || strings.Contains(kind, "application/json") {
		return result, fmt.Errorf("测速源未返回下载数据")
	}
	start := time.Now()
	var durationReached atomic.Bool
	timer := time.AfterFunc(limits.Duration, func() { durationReached.Store(true); cancel() })
	defer timer.Stop()
	writer := &downloadCounter{start: start, progress: progress}
	_, readError := io.CopyBuffer(writer, io.LimitReader(response.Body, limits.MaxBytes), make([]byte, 32<<10))
	timer.Stop()
	result.Bytes, result.Duration = writer.bytes, time.Since(start)
	if readError != nil && !durationReached.Load() {
		return result, fmt.Errorf("下载测速中断，请重试")
	}
	if result.Bytes <= 0 || result.Duration <= 0 {
		return result, fmt.Errorf("未收到可测量的下载数据")
	}
	result.BytesPerSecond = downloadRate(result.Bytes, result.Duration)
	return result, nil
}

type downloadCounter struct {
	bytes       int64
	start, last time.Time
	progress    func(int64, time.Duration)
}

func (c *downloadCounter) Write(chunk []byte) (int, error) {
	c.bytes += int64(len(chunk))
	now := time.Now()
	if c.progress != nil && now.Sub(c.last) >= 200*time.Millisecond {
		c.last = now
		c.progress(c.bytes, now.Sub(c.start))
	}
	return len(chunk), nil
}

func downloadRate(bytes int64, elapsed time.Duration) int64 {
	if elapsed <= 0 {
		return 0
	}
	return int64(float64(bytes) / elapsed.Seconds())
}

func (s *Service) recordDownloadBytes(id string, bytes int64) {
	if bytes <= 0 {
		return
	}
	s.state.Update(func(sn *Snapshot) {
		for _, profile := range sn.Profiles {
			if profile.ID == id {
				if sn.Usage == nil {
					sn.Usage = make(map[string]store.Transfer)
				}
				usage := sn.Usage[id]
				usage.Download += bytes
				sn.Usage[id] = usage
				return
			}
		}
	})
}

func (s *Service) persistDownload(ctx context.Context, selected ProfileView, generation uint64, result store.DownloadSpeed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return fmt.Errorf("测速已取消，原记录未更改")
	}
	if s.state.Snapshot().NodeSpeeds[selected.ID].request != generation {
		return fmt.Errorf("测速已被替换，原记录未更改")
	}
	d, err := s.store.Load()
	if err != nil {
		return fmt.Errorf("无法读取测速记录")
	}
	matched := false
	for _, profile := range d.Profiles {
		if profile.ID == selected.ID && profile.UpdatedAt.Equal(selected.revision) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("节点已变更或删除，未保存本次测速")
	}
	if d.NodeSpeeds == nil {
		d.NodeSpeeds = make(map[string]store.DownloadSpeed)
	}
	d.NodeSpeeds[selected.ID] = result
	d.Usage = s.state.Snapshot().Usage
	if err := s.store.Save(d); err != nil {
		return fmt.Errorf("测速结果保存失败，原记录未更改")
	}
	return nil
}
