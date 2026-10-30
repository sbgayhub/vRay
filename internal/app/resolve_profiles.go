package app

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

// 在有界池中解析服务器地址。外部 IP 元数据只在请求时单独查询；
// DNS 名称永远不会发送给该服务商。
func (s *Service) resolveProfiles(profiles []ProfileView) {
	s.viewMu.Lock()
	if s.dnsCancel != nil {
		s.dnsCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.dnsCancel = cancel
	if s.addresses == nil {
		s.addresses = make(map[string]string)
	}
	s.viewMu.Unlock()
	go func() {
		defer cancel()
		jobs := make(chan ProfileView)
		var workers sync.WaitGroup
		for range min(6, len(profiles)) {
			workers.Go(func() {
				for profile := range jobs {
					if ctx.Err() != nil {
						return
					}
					ip := profile.IP
					if ip == "" {
						if parsed, err := netip.ParseAddr(profile.Address); err == nil {
							ip = parsed.String()
						} else {
							lookup, stop := context.WithTimeout(ctx, 5*time.Second)
							addresses, err := net.DefaultResolver.LookupNetIP(lookup, "ip", profile.Address)
							stop()
							if err != nil {
								continue
							}
							for _, address := range addresses {
								ip = address.Unmap().String()
								if address.Is4() {
									break
								}
							}
						}
					}
					if ip == "" || ctx.Err() != nil {
						continue
					}
					s.viewMu.Lock()
					s.addresses[profile.Address] = ip
					s.viewMu.Unlock()
					s.state.Update(func(sn *Snapshot) {
						for i := range sn.Profiles {
							if sn.Profiles[i].ID == profile.ID && sn.Profiles[i].Address == profile.Address {
								sn.Profiles[i].IP = ip
								break
							}
						}
					})
				}
			})
		}
	send:
		for _, profile := range profiles {
			select {
			case <-ctx.Done():
				break send
			case jobs <- profile:
			}
		}
		close(jobs)
		workers.Wait()
	}()
}
