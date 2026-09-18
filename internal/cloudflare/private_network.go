package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"net/netip"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

type PrivateRoute struct {
	ID               string `json:"id"`
	Network          string `json:"network"`
	TunnelID         string `json:"tunnel_id"`
	Comment          string `json:"comment"`
	VirtualNetworkID string `json:"virtual_network_id,omitempty"`
}

// Default to a single private host. Broader private CIDRs must be explicit and
// contain the service target. Never accidentally advertise a default route.
func PrivateNetwork(target, network string) (netip.Prefix, error) {
	ip, err := netip.ParseAddr(target)
	if err != nil || !ip.IsPrivate() || ip.Is4In6() {
		return netip.Prefix{}, errors.New("WARP 目标必须是私网 IPv4 / IPv6 地址，不能使用主机名或回环地址")
	}
	if network == "" {
		return netip.PrefixFrom(ip, ip.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(network)
	if err != nil || prefix != prefix.Masked() || !prefix.Contains(ip) {
		return netip.Prefix{}, errors.New("私网 CIDR 必须规范化且包含目标 IP")
	}
	for _, allowed := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
		parent := netip.MustParsePrefix(allowed)
		if parent.Addr().BitLen() == prefix.Addr().BitLen() && prefix.Bits() >= parent.Bits() && parent.Contains(prefix.Addr()) {
			return prefix, nil
		}
	}
	return netip.Prefix{}, errors.New("仅允许 RFC 1918 / IPv6 ULA 私网范围")
}

func (p *Publisher) syncPrivateRoute(ctx context.Context, c *Client, connection store.CloudflareConnection, zone Zone, service store.Service, tunnelID string) (SyncResult, error) {
	network, err := PrivateNetwork(service.TargetHost, service.PrivateNetwork)
	if err != nil {
		return SyncResult{}, err
	}
	resource, err := p.Store.CloudflareResource(ctx, connection.ID, service.ID, "private_route")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SyncResult{}, err
	}
	base := "/accounts/" + escaped(zone.Account.ID) + "/teamnet/routes"
	routes, err := listAll[PrivateRoute](ctx, c, base+"?is_deleted=false", 100)
	if err != nil {
		return SyncResult{}, err
	}
	found := false
	for _, route := range routes {
		if route.ID == resource.RemoteID {
			if route.Network != network.String() || route.TunnelID != tunnelID || route.Comment != tunnelName(service.ID) {
				return SyncResult{}, errors.New("私网路由已变更，请先清理旧发布")
			}
			found = true
			continue
		}
		existing, parseErr := netip.ParsePrefix(route.Network)
		if parseErr != nil {
			return SyncResult{}, errors.New("无法解析已有私网路由，不能安全检查冲突")
		}
		if network.Overlaps(existing) {
			return SyncResult{}, errors.New("已有私网路由与此 CIDR 重叠，请在 Cloudflare 核对路由和虚拟网络")
		}
	}
	if resource.RemoteID != "" && !found {
		return SyncResult{}, errors.New("已管理私网路由不存在，请先清理旧发布")
	}
	if !found {
		var route PrivateRoute
		if err = c.do(ctx, http.MethodPost, base, map[string]string{"network": network.String(), "tunnel_id": tunnelID, "comment": tunnelName(service.ID)}, &route); err != nil {
			return SyncResult{}, err
		}
		if route.ID == "" {
			return SyncResult{}, errors.New("Cloudflare 未返回私网路由 ID")
		}
		if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "private_route", RemoteID: route.ID, Hostname: network.String()}); err != nil {
			return SyncResult{}, errors.Join(err, c.do(ctx, http.MethodDelete, base+"/"+escaped(route.ID), nil, nil))
		}
	}
	return SyncResult{ResourceID: tunnelID, TargetURL: network.String()}, nil
}

func (p *Publisher) cleanupPrivateRoute(ctx context.Context, c *Client, zone Zone, service store.Service, resource store.CloudflareResource) error {
	path := "/accounts/" + escaped(zone.Account.ID) + "/teamnet/routes/" + escaped(resource.RemoteID)
	var route PrivateRoute
	if err := c.do(ctx, http.MethodGet, path, nil, &route); err != nil {
		return err
	}
	tunnel, err := p.Store.CloudflareResource(ctx, resource.ConnectionID, service.ID, "tunnel")
	if err != nil {
		return err
	}
	if route.Network != resource.Hostname || route.Comment != tunnelName(service.ID) || route.TunnelID != tunnel.RemoteID {
		return errors.New("私网路由已在外部变更，拒绝删除")
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
