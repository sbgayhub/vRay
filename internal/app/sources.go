package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sbgayhub/vray/internal/diag"
	"github.com/sbgayhub/vray/internal/store"
	"github.com/sbgayhub/vray/internal/suburl"
)

// SourceEdit 只在编辑期间存在；普通列表永远不包含 URL。
type SourceEdit struct {
	ID, Name, URL, UserAgent string
	IntervalMinutes          int
	Enabled                  bool
}

func sourceIndex(d store.Data, id string) int {
	for i := range d.Sources {
		if d.Sources[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Service) sourceProblem(message string) {
	s.state.Update(func(sn *Snapshot) { sn.SourceResult = message })
}

func (s *Service) EditSource(id string) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		d, err := s.store.Load()
		i := sourceIndex(d, id)
		if err != nil || i < 0 {
			s.sourceProblem("找不到订阅")
			return
		}
		src := d.Sources[i]
		raw, err := s.secrets.Get(src.URLRef)
		if err != nil {
			s.sourceProblem("无法读取订阅地址")
			return
		}
		draft := &SourceEdit{ID: id, Name: src.Name, URL: string(raw), UserAgent: src.UserAgent,
			IntervalMinutes: src.IntervalMinutes, Enabled: src.Enabled}
		s.state.Update(func(sn *Snapshot) { sn.SourceDraft = draft; sn.SourceResult = "" })
	}()
}

func (s *Service) CloseSourceEditor() {
	s.state.Update(func(sn *Snapshot) { sn.SourceDraft = nil })
}

func (s *Service) SaveSource(edit SourceEdit) <-chan struct{} {
	s.sourceProblem("")
	return s.runAsync(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		edit.Name, edit.URL = strings.TrimSpace(edit.Name), strings.TrimSpace(edit.URL)
		validInterval := edit.IntervalMinutes == 0 || (edit.IntervalMinutes >= 15 && edit.IntervalMinutes <= 10080)
		if edit.Name == "" || !looksLikeURL(edit.URL) || !validInterval {
			s.sourceProblem("请填写名称和有效的 HTTP(S) 地址；更新间隔为 0 或 15–10080 分钟")
			return
		}
		d, err := s.store.Load()
		i := sourceIndex(d, edit.ID)
		if err != nil || i < 0 {
			s.sourceProblem("找不到订阅")
			return
		}
		src := d.Sources[i]
		key := "source/" + edit.ID + "/url/" + newID()
		if err := s.secrets.Set(key, []byte(edit.URL)); err != nil {
			s.sourceProblem("保存订阅地址失败")
			return
		}
		oldRef := src.URLRef
		src.Name, src.Host, src.URLRef = edit.Name, subscriptionHost(edit.URL), key
		src.UserAgent, src.IntervalMinutes, src.Enabled = edit.UserAgent, edit.IntervalMinutes, edit.Enabled
		src.Revision++
		if err := s.store.PutSource(src); err != nil {
			_ = s.secrets.Delete(key)
			s.sourceProblem("保存订阅失败")
			return
		}
		_ = s.secrets.Delete(oldRef)
		if err := s.Load(); err != nil {
			s.sourceProblem("读取订阅失败")
			return
		}
		s.state.Update(func(sn *Snapshot) { sn.SourceDraft = nil; sn.SourceResult = "订阅已保存" })
	})
}

func (s *Service) DeleteSource(id string, removeNodes bool) <-chan struct{} {
	return s.runAsync(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		d, err := s.store.Load()
		i := sourceIndex(d, id)
		if err != nil || i < 0 {
			s.sourceProblem("找不到订阅")
			return
		}
		snap := s.state.Snapshot()
		for _, p := range d.Profiles {
			if removeNodes && p.SourceID == id && p.ID == snap.Active && snap.Phase == PhaseConnected {
				s.sourceProblem("该订阅的节点正在连接，请先断开或选择保留节点")
				return
			}
		}
		refs := []string{d.Sources[i].URLRef}
		d.Sources = append(d.Sources[:i], d.Sources[i+1:]...)
		groups := []store.PolicyGroup{}
		for _, g := range d.Groups {
			if g.SourceID != id {
				groups = append(groups, g)
			} else if d.Settings.ActiveGroup == g.ID {
				d.Settings.ActiveGroup = ""
			}
		}
		d.Groups = groups
		kept := make([]store.Profile, 0, len(d.Profiles))
		for _, p := range d.Profiles {
			if p.SourceID != id {
				kept = append(kept, p)
				continue
			}
			if !removeNodes {
				p.SourceID = ""
				kept = append(kept, p)
				continue
			}
			refs = append(refs, p.SecretRef)
			if d.Settings.ActiveProfile == p.ID {
				d.Settings.ActiveProfile = ""
			}
		}
		d.Profiles = kept
		if err := s.store.Save(d); err != nil {
			s.sourceProblem("删除订阅失败")
			return
		}
		for _, ref := range refs {
			if ref != "" {
				_ = s.secrets.Delete(ref)
			}
		}
		if err := s.Load(); err != nil {
			s.sourceProblem("读取订阅失败")
			return
		}
		s.state.Update(func(sn *Snapshot) { sn.SourceDraft = nil; sn.SourceResult = "订阅已删除" })
	})
}

func (s *Service) RefreshSource(id string) <-chan struct{} {
	return s.refreshSourceIntent(context.Background(), id)
}

func (s *Service) refreshSourceIntent(parent context.Context, id string) <-chan struct{} {
	done := make(chan struct{})
	started := false
	s.state.Update(func(sn *Snapshot) {
		for i := range sn.Sources {
			if sn.Sources[i].ID == id && !sn.Sources[i].Updating {
				sn.Sources[i].Updating = true
				started = true
			}
		}
	})
	if !started {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.state.Update(func(sn *Snapshot) {
			for i := range sn.Sources {
				if sn.Sources[i].ID == id {
					sn.Sources[i].Updating = false
				}
			}
		})
		if err := s.refreshSource(parent, id); err != nil {
			message := TrimDetail(diag.Scrub(err.Error()))
			s.mu.Lock()
			d, loadErr := s.store.Load()
			i := sourceIndex(d, id)
			if loadErr == nil && i >= 0 {
				d.Sources[i].LastError = message
				_ = s.store.PutSource(d.Sources[i])
				_ = s.Load()
			}
			s.mu.Unlock()
			s.sourceProblem("订阅更新失败：" + message)
		}
	}()
	return done
}

func (s *Service) refreshSource(parent context.Context, id string) error {
	s.mu.Lock()
	d, err := s.store.Load()
	i := sourceIndex(d, id)
	if err != nil || i < 0 {
		s.mu.Unlock()
		return fmt.Errorf("订阅已不存在")
	}
	src := d.Sources[i]
	rawURL, err := s.secrets.Get(src.URLRef)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("无法读取订阅地址")
	}
	ctx, cancel := context.WithTimeout(parent, subscriptionTimeout)
	defer cancel()
	agent := src.UserAgent
	if agent == "" || agent == "vRay/1.0 (Xray client)" {
		agent = subscriptionUserAgent
	}
	body, info, infoOK, err := s.fetchSubscriptionWithAgent(ctx, string(rawURL), agent)
	if err != nil {
		return err
	}
	parsed := suburl.ParseText(string(body))
	if len(parsed.Profiles) == 0 {
		return fmt.Errorf("订阅未返回可用节点，已保留原节点")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err = s.store.Load()
	i = sourceIndex(d, id)
	if err != nil || i < 0 {
		return fmt.Errorf("订阅已删除")
	}
	if d.Sources[i].Revision != src.Revision || d.Sources[i].URLRef != src.URLRef {
		return fmt.Errorf("订阅已修改，请重新更新")
	}
	old := make(map[string][]store.Profile)
	for _, p := range d.Profiles {
		if p.SourceID == id {
			secret, _ := s.secretOf(p)
			identity := profileIdentity(p, secret)
			old[identity] = append(old[identity], p)
		}
	}
	profiles := make([]store.Profile, 0, len(d.Profiles)+len(parsed.Profiles))
	for _, p := range d.Profiles {
		if p.SourceID != id {
			profiles = append(profiles, p)
		}
	}
	newRefs := []string{}
	policyNodes := map[string]string{}
	cleanup := func() {
		for _, ref := range newRefs {
			_ = s.secrets.Delete(ref)
		}
	}
	for _, incoming := range parsed.Profiles {
		originalName := incoming.Name
		key := profileIdentity(incoming, incoming.SecretRef)
		incoming.ID, incoming.SourceID = newID(), id
		if pool := old[key]; len(pool) > 0 {
			previous := pool[0]
			incoming.ID, incoming.Name, incoming.Favorite = previous.ID, previous.Name, previous.Favorite
			if len(pool) == 1 {
				delete(old, key)
			} else {
				old[key] = pool[1:]
			}
		}
		ref := "profile/" + incoming.ID + "/secret/" + newID()
		if err := s.secrets.Set(ref, []byte(incoming.SecretRef)); err != nil {
			cleanup()
			return fmt.Errorf("保存节点凭据失败")
		}
		newRefs = append(newRefs, ref)
		incoming.SecretRef = ref
		policyNodes[originalName] = incoming.ID
		incoming.RawOutbound = withoutOutboundSecret(incoming.RawOutbound)
		profiles = append(profiles, incoming)
	}
	active := s.state.Snapshot()
	for _, pool := range old {
		for _, removed := range pool {
			if removed.ID == active.Active && active.Phase == PhaseConnected {
				removed.SourceID = ""
				profiles = append(profiles, removed)
			} else if d.Settings.ActiveProfile == removed.ID {
				d.Settings.ActiveProfile = ""
			}
		}
	}
	previousProfiles := d.Profiles
	d.Profiles = profiles
	if len(parsed.Groups) > 0 {
		d.Groups = mergePolicyGroups(d.Groups, parsed.Groups, id, policyNodes)
	}
	current := &d.Sources[i]
	current.LastFetchedAt, current.LastError = time.Now(), ""
	current.UserAgent = agent
	current.Revision++
	if infoOK {
		current.Upload, current.Download, current.Total = info.Upload, info.Download, info.Total
		if info.Expire > 0 {
			current.ExpireAt = time.Unix(info.Expire, 0)
		}
	}
	if err := s.store.Save(d); err != nil {
		cleanup()
		return fmt.Errorf("保存订阅失败")
	}
	retained := make(map[string]bool)
	for _, p := range profiles {
		retained[p.SecretRef] = true
	}
	for _, p := range previousProfiles {
		if p.SourceID == id && !retained[p.SecretRef] {
			_ = s.secrets.Delete(p.SecretRef)
		}
	}
	if err := s.Load(); err != nil {
		return err
	}
	s.sourceProblem(fmt.Sprintf("订阅已更新 · %d 个节点", len(parsed.Profiles)))
	return nil
}

func profileIdentity(p store.Profile, secret string) string {
	// 存储美化输出 JSON 时 RawMessage 的空白会变化。计算指纹前先规范化，
	// 并忽略可编辑的显示名称。
	var outbound map[string]any
	if json.Unmarshal(p.RawOutbound, &outbound) == nil {
		delete(outbound, "name")
		delete(outbound, "secret")
	}
	canonical, _ := json.Marshal(outbound)
	return fmt.Sprintf("%s|%s|%d|%x|%x", p.Protocol, p.Address, p.Port, sha256.Sum256(canonical), sha256.Sum256([]byte(secret)))
}

func withoutOutboundSecret(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var outbound map[string]json.RawMessage
	if json.Unmarshal(raw, &outbound) != nil {
		return raw
	}
	delete(outbound, "secret")
	cleaned, err := json.Marshal(outbound)
	if err != nil {
		return raw
	}
	return cleaned
}
