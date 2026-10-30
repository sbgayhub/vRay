package configgen

import "fmt"

const PolicyNodePrefix = "policy-node:"

type PolicyNode struct {
	ID       string
	Outbound Outbound
}
type PolicyGroup struct {
	ID, Name, Type, Selected string
	Members                  []string
}
type PolicyLabel struct{ ID, Tag, Name, Protocol string }
type FallbackPolicy struct {
	Tag     string
	Members []string
}
type PolicyMetadata struct {
	Default   PolicyRoute      `json:"default"`
	Nodes     []PolicyLabel    `json:"nodes,omitempty"`
	Fallbacks []FallbackPolicy `json:"fallbacks,omitempty"`
}

type PolicyRoute struct {
	GroupID, Tag, Kind, Fallback string
	Balanced                     bool
}
type policyBalancer struct {
	Tag         string   `json:"tag"`
	Selector    []string `json:"selector"`
	FallbackTag string   `json:"fallbackTag,omitempty"`
	Strategy    struct {
		Type string `json:"type"`
	} `json:"strategy"`
}
type policyTarget struct {
	tag      string
	kind     string
	balanced bool
	leaves   []string
}

func applyPolicies(cfg *config, in Input) error {
	if in.ActivePolicy == "" || in.Settings.RoutingMode == "direct" {
		return nil
	}
	cfg.Policies = &PolicyMetadata{}
	groups := map[string]PolicyGroup{}
	for _, g := range in.PolicyGroups {
		groups[g.ID] = g
	}
	nodes := map[string]Outbound{}
	for _, n := range in.PolicyNodes {
		nodes[n.ID] = n.Outbound
	}
	resolved := map[string]policyTarget{}
	visiting := map[string]bool{}
	included := map[string]bool{}
	var visit func(string) (policyTarget, error)
	visit = func(id string) (policyTarget, error) {
		if id == "DIRECT" {
			return policyTarget{tag: TagDirect, leaves: []string{TagDirect}}, nil
		}
		if id == "REJECT" || id == "REJECT-DROP" {
			return policyTarget{tag: TagBlock, leaves: []string{TagBlock}}, nil
		}
		if target, ok := resolved[id]; ok {
			return target, nil
		}
		if node, ok := nodes[id]; ok {
			tag := PolicyNodePrefix + id
			if !included[id] {
				cfg.Outbounds = append(cfg.Outbounds, buildProxy(tag, node))
				included[id] = true
				cfg.Policies.Nodes = append(cfg.Policies.Nodes, PolicyLabel{ID: id, Tag: tag, Name: node.Name, Protocol: node.Protocol})
			}
			return policyTarget{tag: tag, leaves: []string{tag}}, nil
		}
		g, ok := groups[id]
		if !ok {
			return policyTarget{}, fmt.Errorf("策略组引用了不存在或不可连接的成员")
		}
		if visiting[id] || len(visiting) > 64 {
			return policyTarget{}, fmt.Errorf("策略组存在循环引用")
		}
		visiting[id] = true
		defer delete(visiting, id)
		if g.Type == "select" {
			member := g.Selected
			if member == "" && len(g.Members) > 0 {
				member = g.Members[0]
			}
			target, err := visit(member)
			if err == nil {
				resolved[id] = target
			}
			return target, err
		}
		members := []string{}
		seen := map[string]bool{}
		for _, member := range g.Members {
			target, err := visit(member)
			if err != nil {
				continue
			}
			for _, leaf := range target.leaves {
				if !seen[leaf] && leaf != TagBlock {
					seen[leaf] = true
					members = append(members, leaf)
				}
			}
		}
		if len(members) == 0 {
			return policyTarget{}, fmt.Errorf("策略组没有可连接的成员")
		}
		b := policyBalancer{Tag: "policy:" + id, Selector: members, FallbackTag: members[0]}
		b.Strategy.Type = "roundRobin"
		if g.Type == "url-test" {
			b.Strategy.Type = "leastPing"
		}
		if g.Type == "fallback" {
			cfg.Policies.Fallbacks = append(cfg.Policies.Fallbacks, FallbackPolicy{Tag: b.Tag, Members: members})
		}
		cfg.Routing.Balancers = append(cfg.Routing.Balancers, b)
		target := policyTarget{tag: b.Tag, kind: g.Type, balanced: true, leaves: members}
		resolved[id] = target
		return target, nil
	}
	target, err := visit(in.ActivePolicy)
	if err != nil {
		return err
	}
	cfg.Policies.Default = PolicyRoute{GroupID: in.ActivePolicy, Tag: target.tag, Kind: target.kind, Balanced: target.balanced}
	if len(target.leaves) > 0 {
		cfg.Policies.Default.Fallback = target.leaves[0]
	}
	rule := routingRule{Type: "field", RuleTag: "policy-default", Network: "tcp,udp"}
	if target.balanced {
		rule.BalancerTag = target.tag
	} else {
		rule.OutboundTag = target.tag
	}
	replaced := false
	for i := range cfg.Routing.Rules {
		if cfg.Routing.Rules[i].OutboundTag == TagProxy {
			cfg.Routing.Rules[i].OutboundTag = ""
			cfg.Routing.Rules[i].BalancerTag = ""
			if target.balanced {
				cfg.Routing.Rules[i].BalancerTag = target.tag
			} else {
				cfg.Routing.Rules[i].OutboundTag = target.tag
			}
		}
		if cfg.Routing.Rules[i].RuleTag == "mode-global" {
			cfg.Routing.Rules[i] = rule
			replaced = true
		}
	}
	if !replaced {
		cfg.Routing.Rules = append(cfg.Routing.Rules, rule)
	}
	return nil
}
