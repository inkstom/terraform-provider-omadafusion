package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

// Objects in this file were verified against a Fusion 2.5G integrated
// controller (6.3.14.2, API 3) by creating, editing and deleting throwaway
// copies. Bodies are built from the controller's own GET shape so fields the
// provider does not manage are sent back unchanged on update.

// ---------- generic helpers ----------

// findByID returns the raw object with the given id field from a paged list.
func findRaw(ctx context.Context, c *Client, path, idKey, id, what string) (map[string]any, error) {
	all, err := listAll[map[string]any](ctx, c, path)
	if err != nil {
		return nil, err
	}
	for _, o := range all {
		if fmt.Sprint(o[idKey]) == id {
			return o, nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("%s %q", what, id)}
}

// createAndFind POSTs body and resolves the new object's ID. The controller
// returns the ID in several shapes, or not at all; in that case the object is
// found by name among objects that did not exist before.
func createAndFind(ctx context.Context, c *Client, path, idKey, name string, body any) (string, error) {
	before, err := listAll[map[string]any](ctx, c, path)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, o := range before {
		if fmt.Sprint(o["name"]) == name {
			return "", fmt.Errorf("an object named %q already exists (id %v); import it instead", name, o[idKey])
		}
		seen[fmt.Sprint(o[idKey])] = true
	}
	var res json.RawMessage
	if err := c.Do(ctx, http.MethodPost, path, body, &res); err != nil {
		return "", err
	}
	if id := idFromResult(res); id != "" {
		return id, nil
	}
	after, err := listAll[map[string]any](ctx, c, path)
	if err != nil {
		return "", err
	}
	for _, o := range after {
		if id := fmt.Sprint(o[idKey]); !seen[id] && fmt.Sprint(o["name"]) == name {
			return id, nil
		}
	}
	return "", fmt.Errorf("%q was created but could not be read back", name)
}

func withoutKeys(m map[string]any, keys ...string) map[string]any {
	out := deepCopy(m)
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

// ---------- LAN networks (full lifecycle) ----------

const lanNetworksPath = "/setting/lan/networks"

// LANNetworkRaw reads one network as the controller returns it.
func (c *Client) LANNetworkRaw(ctx context.Context, siteID, id string) (map[string]any, error) {
	return findRaw(ctx, c, sitePath(siteID, lanNetworksPath), "id", id, "LAN network")
}

// DefaultLANNetworkTemplate is a gateway-served VLAN network body with the
// same defaults the controller UI uses. Callers fill in name, vlan,
// gatewaySubnet, interfaceIds and dhcpSettings.
func DefaultLANNetworkTemplate() map[string]any {
	return map[string]any{
		"purpose": "interface", "vlanType": 0, "application": 0, "isolation": false,
		"igmpSnoopEnable": false, "fastLeaveEnable": false, "mldSnoopEnable": false,
		"dhcpv6Guard": map[string]any{"enable": false}, "dhcpL2RelayEnable": false,
		"dhcpGuard": map[string]any{"enable": false}, "portal": false, "accessControlRule": false,
		"rateLimit": false, "lanNetworkIpv6Config": map[string]any{"proto": "none", "enable": 0},
		"allLan": false, "arpDetectionEnable": false, "qosQueueEnable": false,
	}
}

func fixIPv6(body map[string]any) {
	v6, _ := body["lanNetworkIpv6Config"].(map[string]any)
	if v6 == nil {
		v6 = map[string]any{"enable": 0}
		body["lanNetworkIpv6Config"] = v6
	}
	if _, ok := v6["proto"]; !ok {
		v6["proto"] = "none"
	}
}

// CreateLANNetwork creates a network and returns its ID. The controller also
// adds it as a tagged network to the "All" port profile and creates an
// access profile with the same name.
func (c *Client) CreateLANNetwork(ctx context.Context, siteID string, body map[string]any) (string, error) {
	fixIPv6(body)
	return createAndFind(ctx, c, sitePath(siteID, lanNetworksPath), "id", fmt.Sprint(body["name"]), body)
}

// UpdateLANNetwork PATCHes the complete network object.
func (c *Client) UpdateLANNetwork(ctx context.Context, siteID, id string, body map[string]any) error {
	fixIPv6(body)
	return c.Do(ctx, http.MethodPatch, sitePath(siteID, lanNetworksPath+"/"+url.PathEscape(id)), body, nil)
}

// DeleteLANNetwork deletes a network.
func (c *Client) DeleteLANNetwork(ctx context.Context, siteID, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, lanNetworksPath+"/"+url.PathEscape(id)), nil, nil)
}

// ---------- Switch port profiles ----------

const portProfilesPath = "/setting/lan/profiles"

// PortProfiles lists switch port profiles.
func (c *Client) PortProfiles(ctx context.Context, siteID string) ([]map[string]any, error) {
	return listAll[map[string]any](ctx, c, sitePath(siteID, portProfilesPath))
}

// PortProfileRaw reads one profile by ID.
func (c *Client) PortProfileRaw(ctx context.Context, siteID, id string) (map[string]any, error) {
	return findRaw(ctx, c, sitePath(siteID, portProfilesPath), "id", id, "port profile")
}

// PortProfileByName finds a profile by name or ID.
func (c *Client) PortProfileByName(ctx context.Context, siteID, key string) (map[string]any, error) {
	all, err := c.PortProfiles(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if p["name"] == key || p["id"] == key {
			return p, nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("port profile %q", key)}
}

// DefaultPortProfileTemplate mirrors the defaults of a new profile in the UI.
func DefaultPortProfileTemplate() map[string]any {
	return map[string]any{
		"poe": 2, "tagNetworkIds": []any{}, "untagNetworkIds": []any{}, "dot1x": 2,
		"portIsolationEnable": false, "lldpMedEnable": true, "topoNotifyEnable": false,
		"bandWidthCtrlType": 0, "spanningTreeEnable": true,
		"spanningTreeSetting": map[string]any{"priority": 128, "extPathCost": 0, "intPathCost": 0, "edgePort": false,
			"p2pLink": 0, "mcheck": false, "loopProtect": false, "rootProtect": false, "tcGuard": false,
			"bpduProtect": false, "bpduFilter": false, "bpduForward": true},
		"loopbackDetectEnable": false, "eeeEnable": false, "flowControlEnable": false, "fastLeaveEnable": false,
		"loopbackDetectVlanBasedEnable": false, "igmpFastLeaveEnable": false, "mldFastLeaveEnable": false,
		"dhcpL2RelaySettings": map[string]any{"enable": false}, "dot1pPriority": 0, "trustMode": 0,
	}
}

var portProfileReadOnly = []string{"id", "site", "flag", "prohibitModify", "type", "supportESEnable", "networkConflict", "resource", "esTaggedModified"}

// PortProfileWriteBody strips read-only fields from a GET object.
func PortProfileWriteBody(raw map[string]any) map[string]any {
	return withoutKeys(raw, portProfileReadOnly...)
}

// CreatePortProfile creates a profile and returns its ID.
func (c *Client) CreatePortProfile(ctx context.Context, siteID string, body map[string]any) (string, error) {
	return createAndFind(ctx, c, sitePath(siteID, portProfilesPath), "id", fmt.Sprint(body["name"]), body)
}

// UpdatePortProfile PATCHes a profile.
func (c *Client) UpdatePortProfile(ctx context.Context, siteID, id string, body map[string]any) error {
	return c.Do(ctx, http.MethodPatch, sitePath(siteID, portProfilesPath+"/"+url.PathEscape(id)), body, nil)
}

// DeletePortProfile deletes a profile.
func (c *Client) DeletePortProfile(ctx context.Context, siteID, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, portProfilesPath+"/"+url.PathEscape(id)), nil, nil)
}

// ---------- Switch ports ----------

// SwitchPort is the subset of a switch port the provider manages.
type SwitchPort struct {
	Port        int    `json:"port"`
	Name        string `json:"name"`
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
	Override    bool   `json:"profileOverrideEnable"`
}

// SwitchPorts lists the ports of an adopted switch.
func (c *Client) SwitchPorts(ctx context.Context, siteID, mac string) ([]SwitchPort, error) {
	var out []SwitchPort
	err := c.Do(ctx, http.MethodGet, sitePath(siteID, "/switches/"+url.PathEscape(NormalizeMAC(mac))+"/ports"), nil, &out)
	return out, err
}

// SwitchPort reads one port.
func (c *Client) SwitchPort(ctx context.Context, siteID, mac string, port int) (*SwitchPort, error) {
	all, err := c.SwitchPorts(ctx, siteID, mac)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Port == port {
			return &all[i], nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("port %d on switch %s", port, mac)}
}

// SetSwitchPort assigns a name and profile to a port. Only these fields are
// sent; the controller ignores per-port disable and PoE unless the profile is
// overridden, so those are managed through profiles instead.
func (c *Client) SetSwitchPort(ctx context.Context, siteID, mac string, port int, name, profileID string) error {
	body := map[string]any{"name": name, "profileId": profileID, "profileOverrideEnable": false}
	return c.Do(ctx, http.MethodPatch, sitePath(siteID, fmt.Sprintf("/switches/%s/ports/%d", url.PathEscape(NormalizeMAC(mac)), port)), body, nil)
}

// ---------- Port forwarding ----------

const portForwardingsPath = "/setting/transmission/portForwardings"

// PortForwarding is a NAT virtual-server rule.
type PortForwarding struct {
	ID               string   `json:"id,omitempty"`
	Name             string   `json:"name"`
	Status           bool     `json:"status"`
	From             int      `json:"from"` // 0 anywhere, 1 limited
	LimitedAddresses []string `json:"limitedAddresses,omitempty"`
	WANPortIDs       []string `json:"interfaceWanPortId"`
	ExternalPort     string   `json:"externalPort"`
	ForwardIP        string   `json:"forwardIp"`
	ForwardPort      string   `json:"forwardPort"`
	Protocol         int      `json:"protocol"` // 0 all, 1 tcp, 2 udp
	DMZ              bool     `json:"dMZ"`
}

// PortForwarding reads one rule by ID.
func (c *Client) PortForwarding(ctx context.Context, siteID, id string) (*PortForwarding, error) {
	raw, err := findRaw(ctx, c, sitePath(siteID, portForwardingsPath), "id", id, "port forwarding rule")
	if err != nil {
		return nil, err
	}
	var pf PortForwarding
	return &pf, remarshal(raw, &pf)
}

func (pf PortForwarding) body() PortForwarding {
	pf.ID = ""
	if pf.From == 0 {
		pf.LimitedAddresses = nil
	}
	return pf
}

// CreatePortForwarding creates a rule and returns its ID.
func (c *Client) CreatePortForwarding(ctx context.Context, siteID string, pf PortForwarding) (string, error) {
	return createAndFind(ctx, c, sitePath(siteID, portForwardingsPath), "id", pf.Name, pf.body())
}

// UpdatePortForwarding replaces a rule (PUT; PATCH is not supported).
func (c *Client) UpdatePortForwarding(ctx context.Context, siteID, id string, pf PortForwarding) error {
	return c.Do(ctx, http.MethodPut, sitePath(siteID, portForwardingsPath+"/"+url.PathEscape(id)), pf.body(), nil)
}

// DeletePortForwarding deletes a rule.
func (c *Client) DeletePortForwarding(ctx context.Context, siteID, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, portForwardingsPath+"/"+url.PathEscape(id)), nil, nil)
}

// ---------- Static routes ----------

const staticRoutesPath = "/setting/transmission/staticRoutings"

// StaticRoute is a gateway static route. The controller does not return
// interfaceId on GET.
type StaticRoute struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name"`
	Status       bool     `json:"status"`
	Destinations []string `json:"destinations"`
	RouteType    int      `json:"routeType"` // 0 next hop
	InterfaceID  string   `json:"interfaceId,omitempty"`
	NextHopIP    string   `json:"nextHopIp"`
	Metric       int      `json:"metric"`
}

// StaticRoute reads one route by ID.
func (c *Client) StaticRoute(ctx context.Context, siteID, id string) (*StaticRoute, error) {
	raw, err := findRaw(ctx, c, sitePath(siteID, staticRoutesPath), "id", id, "static route")
	if err != nil {
		return nil, err
	}
	var r StaticRoute
	return &r, remarshal(raw, &r)
}

// CreateStaticRoute creates a route and returns its ID.
func (c *Client) CreateStaticRoute(ctx context.Context, siteID string, r StaticRoute) (string, error) {
	r.ID = ""
	return createAndFind(ctx, c, sitePath(siteID, staticRoutesPath), "id", r.Name, r)
}

// UpdateStaticRoute replaces a route (PUT).
func (c *Client) UpdateStaticRoute(ctx context.Context, siteID, id string, r StaticRoute) error {
	r.ID = ""
	return c.Do(ctx, http.MethodPut, sitePath(siteID, staticRoutesPath+"/"+url.PathEscape(id)), r, nil)
}

// DeleteStaticRoute deletes a route.
func (c *Client) DeleteStaticRoute(ctx context.Context, siteID, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, staticRoutesPath+"/"+url.PathEscape(id)), nil, nil)
}

// ---------- WAN ports ----------

// WANPort is one gateway port as listed under Internet settings.
type WANPort struct {
	Port   int    `json:"port"`
	UUID   string `json:"portUuid"`
	Name   string `json:"portName"`
	Type   int    `json:"type"`
	Status int    `json:"status"`
	IPv4   string `json:"ipv4Addr"`
	Online int    `json:"online"`
}

// WANPorts lists the gateway's WAN-capable ports.
func (c *Client) WANPorts(ctx context.Context, siteID string) ([]WANPort, error) {
	var res struct {
		PortInfo struct {
			Ports []WANPort `json:"wanLanPortSettings"`
		} `json:"osgPortInfo"`
		WANPortSettings []WANPort `json:"wanPortSettings"`
	}
	if err := c.Do(ctx, http.MethodGet, sitePath(siteID, "/setting/wan/networks"), nil, &res); err != nil {
		return nil, err
	}
	if len(res.PortInfo.Ports) > 0 {
		return res.PortInfo.Ports, nil
	}
	return res.WANPortSettings, nil
}

// ---------- SSIDs (full lifecycle) ----------

func ssidsPath(siteID, wlanID string) string {
	return sitePath(siteID, "/setting/wlans/"+url.PathEscape(wlanID)+"/ssids")
}

// DefaultSSIDTemplate is a WPA-Personal SSID with the controller's UI
// defaults. rateLimit must be present or create fails.
func DefaultSSIDTemplate() map[string]any {
	return map[string]any{
		"band": 3, "guestNetEnable": false, "security": 3, "broadcast": true, "vlanEnable": false,
		"pskSetting":         map[string]any{"versionPsk": 2, "encryptionPsk": 3, "gikRekeyPskEnable": false},
		"wlanScheduleEnable": false, "macFilterEnable": false, "enable11r": false, "pmfMode": 3,
		"greEnable": false, "mloEnable": false, "prohibitWifiShare": false,
		"rateLimit":         map[string]any{"downLimitEnable": false, "upLimitEnable": false},
		"ssidRateLimit":     map[string]any{"downLimitEnable": false, "upLimitEnable": false},
		"rateAndBeaconCtrl": map[string]any{"rate2gCtrlEnable": false, "rate5gCtrlEnable": false, "rate6gCtrlEnable": false},
		"multiCastSetting":  map[string]any{"multiCastEnable": true, "channelUtil": 100, "arpCastEnable": true, "ipv6CastEnable": true, "filterEnable": false},
		"dhcpOption82":      map[string]any{"dhcpEnable": false},
		"vlanSetting":       map[string]any{"mode": 0},
	}
}

// SSIDWriteBody strips read-only fields from a GET object.
func SSIDWriteBody(raw map[string]any) map[string]any {
	return withoutKeys(raw, "id", "idInt", "index", "site", "wlanId", "featureDescription", "resource", "deviceType", "showOnScreen")
}

// CreateSSID creates an SSID in a WLAN group and returns its ID.
func (c *Client) CreateSSID(ctx context.Context, siteID, wlanID string, body map[string]any) (string, error) {
	return createAndFind(ctx, c, ssidsPath(siteID, wlanID), "id", fmt.Sprint(body["name"]), body)
}

// UpdateSSID PATCHes an SSID with a complete body.
func (c *Client) UpdateSSID(ctx context.Context, siteID, wlanID, id string, body map[string]any) error {
	return c.Do(ctx, http.MethodPatch, ssidsPath(siteID, wlanID)+"/"+url.PathEscape(id), body, nil)
}

// DeleteSSID deletes an SSID.
func (c *Client) DeleteSSID(ctx context.Context, siteID, wlanID, id string) error {
	return c.Do(ctx, http.MethodDelete, ssidsPath(siteID, wlanID)+"/"+url.PathEscape(id), nil, nil)
}

// ---------- Gateway ACL order ----------

// ReorderGatewayACLs moves the listed rules to the top of the gateway ACL
// table in the given order; all other rules keep their relative order after
// them. Indexes are 1-based.
func (c *Client) ReorderGatewayACLs(ctx context.Context, siteID string, first []string) error {
	all, err := c.GatewayACLs(ctx, siteID)
	if err != nil {
		return err
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Index < all[j].Index })
	want := map[string]bool{}
	order := make([]string, 0, len(all))
	for _, id := range first {
		want[id] = true
	}
	exists := map[string]bool{}
	for _, a := range all {
		exists[a.ID] = true
	}
	for _, id := range first {
		if !exists[id] {
			return &NotFoundError{What: fmt.Sprintf("gateway ACL %q", id)}
		}
		order = append(order, id)
	}
	for _, a := range all {
		if !want[a.ID] {
			order = append(order, a.ID)
		}
	}
	indexes := map[string]int{}
	for i, id := range order {
		indexes[id] = i + 1
	}
	return c.Do(ctx, http.MethodPost, sitePath(siteID, "/cmd/acls/modifyIndex"), map[string]any{"type": 0, "indexes": indexes}, nil)
}
