package app

import (
	"sort"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// ruleViews 把已存储规则转换为页面绘制的内容，并标注每条规则的首个问题。
//
// 标注在这里计算而不是在视图中计算：视图不能每帧逐行校验，而且同一份
// 判定也决定 SaveRule 是否接受该规则。
func ruleViews(rules []store.Rule) []RuleView {
	sorted := append([]store.Rule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })

	out := make([]RuleView, 0, len(sorted))
	for _, r := range sorted {
		v := RuleView{
			ID: r.ID, Name: r.Name, Enabled: r.Enabled,
			Domains: r.Domains, IPs: r.IPs, Ports: r.Ports,
			Process: r.Process, Networks: r.Networks, Outbound: r.Outbound,
		}
		if problems := configgen.ValidateRule(toConfigRule(r)); len(problems) > 0 {
			v.Problem = problems[0].Value + "：" + problems[0].Message
		}
		out = append(out, v)
	}
	return out
}

// toConfigRule 把已存储规则转换为生成器可编译的形式。
func toConfigRule(r store.Rule) configgen.Rule {
	return configgen.Rule{
		ID: r.ID, Name: r.Name,
		Domains: r.Domains, IPs: r.IPs, Ports: r.Ports,
		Process: r.Process, Networks: r.Networks,
		Outbound: r.Outbound,
	}
}

// configRules 按顺序转换所有已启用的存储规则。
//
// 禁用的规则会被完全省略：把它放进配置并依赖路由器跳过，会让“已禁用”
// 成为配置文本并不支持的谎言。
func configRules(rules []store.Rule) []configgen.Rule {
	sorted := append([]store.Rule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })

	out := make([]configgen.Rule, 0, len(sorted))
	for _, r := range sorted {
		if !r.Enabled {
			continue
		}
		out = append(out, toConfigRule(r))
	}
	return out
}

// SaveRules 替换整个规则列表并重新加载快照。
//
// 整个列表一次性写入，因为顺序就是内容：上移一条规则会改变重叠流量由
// 谁胜出；只保存单条会让存储顺序与屏幕不一致。
func (s *Service) SaveRules(rules []store.Rule) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.saveRulesLocked(rules)
	}()
}

// saveRulesLocked 校验并保存整份规则列表，然后刷新快照。调用方必须持有
// s.mu。返回是否成功保存。
func (s *Service) saveRulesLocked(rules []store.Rule) bool {
	// 语法错误的规则在这里拒绝，而不是等到连接时：那时用户收到的
	// 消息针对整份配置。
	for _, r := range rules {
		if problems := configgen.ValidateRule(toConfigRule(r)); len(problems) > 0 {
			s.setProblem("RULE_INVALID", problems[0].Value+"："+problems[0].Message, nil)
			return false
		}
		switch r.Outbound {
		case configgen.TagDirect, configgen.TagProxy, configgen.TagBlock:
		default:
			s.setProblem("RULE_INVALID", "规则的出口必须是 direct、proxy 或 block", nil)
			return false
		}
	}

	if err := s.store.SetRules(rules); err != nil {
		s.setProblem("STORE_ERROR", "保存规则失败", err)
		return false
	}
	if err := s.Load(); err != nil {
		s.setProblem("STORE_ERROR", "读取配置失败", err)
		return false
	}
	return true
}

// mutateRules 在后台对规则列表做一次原子的读-改-写，mutate 返回 nil 表示
// 无需写回。
//
// 读列表、计算新列表、写回必须发生在同一把锁内：快速连点（如连续上移）
// 会让两次操作读到同一份列表，后写覆盖先写丢一次操作；与删除交错时，
// 已删的规则会被写回。
func (s *Service) mutateRules(mutate func(rules []store.Rule) []store.Rule) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		d, err := s.store.Load()
		if err != nil {
			s.setProblem("STORE_ERROR", "读取配置失败", err)
			return
		}
		next := mutate(d.Rules)
		if next == nil {
			return
		}
		s.saveRulesLocked(next)
	}()
}

// SaveRule 插入或替换一条规则，其余规则保持原位。
//
// 新规则追加到末尾：追加是唯一不会悄悄改变屏幕上现有规则含义的位置。
func (s *Service) SaveRule(r store.Rule) {
	s.mutateRules(func(rules []store.Rule) []store.Rule {
		next := append([]store.Rule(nil), rules...)
		replaced := false
		for i := range next {
			if next[i].ID == r.ID {
				r.Order = next[i].Order
				next[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			r.Order = len(next)
			next = append(next, r)
		}
		return next
	})
}

// DeleteRule 删除一条规则并重新编号其余规则。
func (s *Service) DeleteRule(id string) {
	s.mutateRules(func(rules []store.Rule) []store.Rule {
		next := make([]store.Rule, 0, len(rules))
		for _, r := range rules {
			if r.ID != id {
				next = append(next, r)
			}
		}
		return next
	})
}

// MoveRule 把一条规则上移或下移一位。
//
// 一次只移动一位：页面绘制上/下按钮；若允许拖拽到任意目标，当列表超出
// 屏幕后就可能落到错误的行。
func (s *Service) MoveRule(id string, up bool) {
	s.mutateRules(func(rules []store.Rule) []store.Rule {
		ordered := append([]store.Rule(nil), rules...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Order < ordered[j].Order })

		idx := -1
		for i := range ordered {
			if ordered[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			s.setProblem("NO_RULE", "找不到该规则", nil)
			return nil
		}
		target := idx - 1
		if !up {
			target = idx + 1
		}
		if target < 0 || target >= len(ordered) {
			return nil // 已到边界：无需处理，也不是错误
		}
		ordered[idx], ordered[target] = ordered[target], ordered[idx]
		return ordered
	})
}

// SetRuleEnabled 开启或关闭一条规则。
func (s *Service) SetRuleEnabled(id string, on bool) {
	s.mutateRules(func(rules []store.Rule) []store.Rule {
		next := append([]store.Rule(nil), rules...)
		for i := range next {
			if next[i].ID == id {
				next[i].Enabled = on
			}
		}
		return next
	})
}
