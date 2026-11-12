package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Site is a controller site.
type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

// Sites lists all sites visible to the logged-in account.
func (c *Client) Sites(ctx context.Context) ([]Site, error) {
	return listAll[Site](ctx, c, "/sites")
}

// SiteByName resolves a site name to its object. An exact ID match is also accepted.
func (c *Client) SiteByName(ctx context.Context, name string) (*Site, error) {
	sites, err := c.Sites(ctx)
	if err != nil {
		return nil, err
	}
	for i := range sites {
		if sites[i].Name == name || sites[i].ID == name {
			return &sites[i], nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("site %q", name)}
}

func sitePath(siteID, rest string) string {
	return "/sites/" + url.PathEscape(siteID) + rest
}

// Device is an adopted device (gateway, switch or AP).
type Device struct {
	MAC             string `json:"mac"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Model           string `json:"model"`
	IP              string `json:"ip"`
	Status          int    `json:"status"`
	StatusCategory  int    `json:"statusCategory"`
	FirmwareVersion string `json:"firmwareVersion"`
}

// Devices lists the adopted devices of a site.
func (c *Client) Devices(ctx context.Context, siteID string) ([]Device, error) {
	var out []Device
	if err := c.Do(ctx, http.MethodGet, sitePath(siteID, "/devices"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- LAN networks ----------

// LANNetwork is the subset of a LAN network the provider reads. The full
// object is kept in Raw so updates can send it back unchanged.
type LANNetwork struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OrigName      string `json:"origName"`
	VLAN          int    `json:"vlan"`
	GatewaySubnet string `json:"gatewaySubnet"`
	Purpose       string `json:"purpose"`
	Isolation     bool   `json:"isolation"`
	DHCPSettings  struct {
		Enable      bool   `json:"enable"`
		DNSMode     string `json:"dhcpns"`
		PrimaryDNS  string `json:"priDns"`
		SecondaryDN string `json:"sndDns"`
		LeaseTime   int    `json:"leasetime"`
		IPStart     string `json:"ipaddrStart"`
		IPEnd       string `json:"ipaddrEnd"`
	} `json:"dhcpSettings"`
	Raw map[string]any `json:"-"`
}

// DisplayName is the user-facing network name. The default network is
// reported as e.g. "Management(Default)" with origName "Management".
func (n LANNetwork) DisplayName() string {
	if n.OrigName != "" {
		return n.OrigName
	}
	return n.Name
}

// LANNetworks lists the site's LAN networks.
func (c *Client) LANNetworks(ctx context.Context, siteID string) ([]LANNetwork, error) {
	raws, err := listAll[map[string]any](ctx, c, sitePath(siteID, "/setting/lan/networks"))
	if err != nil {
		return nil, err
	}
	out := make([]LANNetwork, 0, len(raws))
	for _, r := range raws {
		var n LANNetwork
		if err := remarshal(r, &n); err != nil {
			return nil, err
		}
		n.Raw = r
		out = append(out, n)
	}
	return out, nil
}

// LANNetwork finds one network by ID, name or original name.
func (c *Client) LANNetwork(ctx context.Context, siteID, key string) (*LANNetwork, error) {
	nets, err := c.LANNetworks(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range nets {
		n := nets[i]
		if n.ID == key || n.Name == key || n.OrigName == key {
			return &n, nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("LAN network %q", key)}
}

// SetLANNetworkDHCPDNS changes only the DHCP DNS fields of a network. The
// controller requires the complete object on PATCH, so it is read first.
// mode is "auto" (hand out the gateway) or "manual".
func (c *Client) SetLANNetworkDHCPDNS(ctx context.Context, siteID, networkID, mode, primary, secondary string) error {
	n, err := c.LANNetwork(ctx, siteID, networkID)
	if err != nil {
		return err
	}
	body := deepCopy(n.Raw)
	dhcp, _ := body["dhcpSettings"].(map[string]any)
	if dhcp == nil {
		return fmt.Errorf("network %s has no dhcpSettings; is DHCP served by the gateway?", networkID)
	}
	dhcp["dhcpns"] = mode
	if mode == "manual" {
		dhcp["priDns"] = primary
		dhcp["sndDns"] = secondary
	} else {
		delete(dhcp, "priDns")
		delete(dhcp, "sndDns")
	}
	// 6.3.x rejects the PATCH with "proto should not be null" unless the
	// IPv6 block carries a proto, even when IPv6 is disabled.
	v6, _ := body["lanNetworkIpv6Config"].(map[string]any)
	if v6 == nil {
		v6 = map[string]any{"enable": 0}
		body["lanNetworkIpv6Config"] = v6
	}
	if _, ok := v6["proto"]; !ok {
		v6["proto"] = "none"
	}
	return c.Do(ctx, http.MethodPatch, sitePath(siteID, "/setting/lan/networks/"+url.PathEscape(networkID)), body, nil)
}

// ---------- WLANs and SSIDs ----------

// WLANGroup is a WLAN group (the container SSIDs belong to).
type WLANGroup struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Primary bool   `json:"primary"`
}

// SSID is the subset of an SSID the provider reads; Raw holds the full object.
type SSID struct {
	ID             string         `json:"id"`
	WLANID         string         `json:"wlanId"`
	Name           string         `json:"name"`
	Band           int            `json:"band"`
	Security       int            `json:"security"`
	Broadcast      bool           `json:"broadcast"`
	VLANEnable     bool           `json:"vlanEnable"`
	GuestNetEnable bool           `json:"guestNetEnable"`
	ScheduleEnable bool           `json:"wlanScheduleEnable"`
	ScheduleAction *int           `json:"action,omitempty"`
	ScheduleID     string         `json:"scheduleId,omitempty"`
	Raw            map[string]any `json:"-"`
}

// WLANGroups lists WLAN groups.
func (c *Client) WLANGroups(ctx context.Context, siteID string) ([]WLANGroup, error) {
	return listAll[WLANGroup](ctx, c, sitePath(siteID, "/setting/wlans"))
}

// SSIDs lists SSIDs in a WLAN group.
func (c *Client) SSIDs(ctx context.Context, siteID, wlanID string) ([]SSID, error) {
	raws, err := listAll[map[string]any](ctx, c, sitePath(siteID, "/setting/wlans/"+url.PathEscape(wlanID)+"/ssids"))
	if err != nil {
		return nil, err
	}
	out := make([]SSID, 0, len(raws))
	for _, r := range raws {
		var s SSID
		if err := remarshal(r, &s); err != nil {
			return nil, err
		}
		if s.WLANID == "" {
			s.WLANID = wlanID
		}
		s.Raw = r
		out = append(out, s)
	}
	return out, nil
}

// FindSSID looks an SSID up by ID or name across all WLAN groups, or within
// one group when wlanID is set.
func (c *Client) FindSSID(ctx context.Context, siteID, wlanID, key string) (*SSID, error) {
	groups := []string{wlanID}
	if wlanID == "" {
		gs, err := c.WLANGroups(ctx, siteID)
		if err != nil {
			return nil, err
		}
		groups = groups[:0]
		for _, g := range gs {
			groups = append(groups, g.ID)
		}
	}
	for _, g := range groups {
		ss, err := c.SSIDs(ctx, siteID, g)
		if err != nil {
			return nil, err
		}
		for i := range ss {
			if ss[i].ID == key || ss[i].Name == key {
				return &ss[i], nil
			}
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("SSID %q", key)}
}

// ssidReadOnlyKeys are returned by GET but are not part of the update body.
var ssidReadOnlyKeys = []string{"featureDescription", "idInt"}

// SetSSIDSchedule enables or disables the WLAN schedule on an existing SSID,
// leaving every other SSID field unchanged. action 1 keeps the radio on
// inside the time range (off outside it); 0 turns it off inside the range.
func (c *Client) SetSSIDSchedule(ctx context.Context, siteID, wlanID, ssidID string, enable bool, action int, timeRangeID string) error {
	s, err := c.FindSSID(ctx, siteID, wlanID, ssidID)
	if err != nil {
		return err
	}
	body := deepCopy(s.Raw)
	for _, k := range ssidReadOnlyKeys {
		delete(body, k)
	}
	body["wlanScheduleEnable"] = enable
	if enable {
		body["action"] = action
		body["scheduleId"] = timeRangeID
	}
	path := sitePath(siteID, "/setting/wlans/"+url.PathEscape(s.WLANID)+"/ssids/"+url.PathEscape(s.ID))
	return c.Do(ctx, http.MethodPatch, path, body, nil)
}

// ---------- Time ranges ----------

// TimeRangePeriod is one on-interval in a time range.
type TimeRangePeriod struct {
	DayType    int `json:"dayType"`
	StartTimeH int `json:"startTimeH"`
	StartTimeM int `json:"startTimeM"`
	EndTimeH   int `json:"endTimeH"`
	EndTimeM   int `json:"endTimeM"`
}

// TimeRange is a time-range profile used by WLAN schedules and ACLs.
type TimeRange struct {
	ID       string            `json:"id,omitempty"`
	Name     string            `json:"name"`
	DayMode  int               `json:"dayMode"`
	DayMon   bool              `json:"dayMon"`
	DayTue   bool              `json:"dayTue"`
	DayWed   bool              `json:"dayWed"`
	DayThu   bool              `json:"dayThu"`
	DayFri   bool              `json:"dayFri"`
	DaySat   bool              `json:"daySat"`
	DaySun   bool              `json:"daySun"`
	TimeList []TimeRangePeriod `json:"timeList"`
}

const timeRangesPath = "/setting/profiles/timeranges"

// TimeRanges lists time-range profiles.
func (c *Client) TimeRanges(ctx context.Context, siteID string) ([]TimeRange, error) {
	return listAll[TimeRange](ctx, c, sitePath(siteID, timeRangesPath))
}

// TimeRange reads one profile by ID.
func (c *Client) TimeRange(ctx context.Context, siteID, id string) (*TimeRange, error) {
	all, err := c.TimeRanges(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("time range %q", id)}
}

// CreateTimeRange creates a profile and returns it with its ID. Names must be
// unique; the ID is resolved by name when the create response omits it.
func (c *Client) CreateTimeRange(ctx context.Context, siteID string, tr TimeRange) (*TimeRange, error) {
	tr.ID = ""
	var res json.RawMessage
	if err := c.Do(ctx, http.MethodPost, sitePath(siteID, timeRangesPath), tr, &res); err != nil {
		return nil, err
	}
	if id := idFromResult(res); id != "" {
		return c.TimeRange(ctx, siteID, id)
	}
	all, err := c.TimeRanges(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == tr.Name {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("time range %q was created but could not be read back", tr.Name)
}

// DeleteTimeRange deletes a profile.
func (c *Client) DeleteTimeRange(ctx context.Context, siteID, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, timeRangesPath+"/"+url.PathEscape(id)), nil, nil)
}

// ---------- DHCP reservations ----------

// DHCPReservation is a fixed address for a MAC on a gateway-served network.
// Reservations are keyed by MAC in the API.
type DHCPReservation struct {
	MAC                  string `json:"mac"`
	IP                   string `json:"ip"`
	NetID                string `json:"netId"`
	Description          string `json:"description"`
	Status               bool   `json:"status"`
	ExportToIPMACBinding bool   `json:"exportToIpMacBinding"`
	Options              []any  `json:"options"`
	NetName              string `json:"netName,omitempty"`
	ClientName           string `json:"clientName,omitempty"`
}

const dhcpPath = "/setting/service/dhcp"

// NormalizeMAC returns a MAC in the controller's AA-BB-CC-DD-EE-FF form.
func NormalizeMAC(mac string) string {
	m := strings.ToUpper(strings.TrimSpace(mac))
	return strings.NewReplacer(":", "-", ".", "-").Replace(m)
}

// DHCPReservations lists reservations.
func (c *Client) DHCPReservations(ctx context.Context, siteID string) ([]DHCPReservation, error) {
	return listAll[DHCPReservation](ctx, c, sitePath(siteID, dhcpPath))
}

// DHCPReservation reads one reservation by MAC.
func (c *Client) DHCPReservation(ctx context.Context, siteID, mac string) (*DHCPReservation, error) {
	mac = NormalizeMAC(mac)
	all, err := c.DHCPReservations(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if NormalizeMAC(all[i].MAC) == mac {
			return &all[i], nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("DHCP reservation for %s", mac)}
}

func reservationBody(r DHCPReservation) map[string]any {
	opts := r.Options
	if opts == nil {
		opts = []any{}
	}
	return map[string]any{
		"mac":                  NormalizeMAC(r.MAC),
		"ip":                   r.IP,
		"netId":                r.NetID,
		"description":          r.Description,
		"status":               r.Status,
		"options":              opts,
		"exportToIpMacBinding": r.ExportToIPMACBinding,
	}
}

// CreateDHCPReservation creates a reservation.
func (c *Client) CreateDHCPReservation(ctx context.Context, siteID string, r DHCPReservation) error {
	return c.Do(ctx, http.MethodPost, sitePath(siteID, dhcpPath), reservationBody(r), nil)
}

// UpdateDHCPReservation replaces a reservation. The MAC cannot change.
func (c *Client) UpdateDHCPReservation(ctx context.Context, siteID string, r DHCPReservation) error {
	return c.Do(ctx, http.MethodPut, sitePath(siteID, dhcpPath+"/"+url.PathEscape(NormalizeMAC(r.MAC))), reservationBody(r), nil)
}

// DeleteDHCPReservation deletes a reservation.
func (c *Client) DeleteDHCPReservation(ctx context.Context, siteID, mac string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, dhcpPath+"/"+url.PathEscape(NormalizeMAC(mac))), nil, nil)
}

// ---------- Gateway ACLs ----------

// ACLStates selects connection states for stateful matching.
type ACLStates struct {
	New         bool `json:"new"`
	Established bool `json:"established"`
	Invalid     bool `json:"invalid"`
	Related     bool `json:"related"`
}

// ACLDirection is the legacy direction object used on write.
type ACLDirection struct {
	WANInIDs []string `json:"wanInIds"`
	VPNInIDs []string `json:"vpnInIds"`
	LANToWAN bool     `json:"lanToWan"`
	LANToLAN bool     `json:"lanToLan"`
}

// GatewayACL is a rule in the site's gateway ACL table (type 0).
type GatewayACL struct {
	ID              string        `json:"id,omitempty"`
	Name            string        `json:"name"`
	Type            int           `json:"type"`
	Index           int           `json:"index,omitempty"`
	Status          bool          `json:"status"`
	Policy          int           `json:"policy"`
	Protocols       []int         `json:"protocols"`
	SourceType      int           `json:"sourceType"`
	SourceIDs       []string      `json:"sourceIds"`
	DestinationType int           `json:"destinationType"`
	DestinationIDs  []string      `json:"destinationIds"`
	Direction       *ACLDirection `json:"direction,omitempty"`
	StateMode       int           `json:"stateMode"`
	States          *ACLStates    `json:"states,omitempty"`
	TimeRangeEnable bool          `json:"timeRangeEnable"`
	TimeRangeID     string        `json:"timeRangeId,omitempty"`
	Syslog          bool          `json:"syslog"`
}

const aclPath = "/setting/firewall/acls"

// GatewayACLs lists gateway ACL rules in table order.
func (c *Client) GatewayACLs(ctx context.Context, siteID string) ([]GatewayACL, error) {
	return listAll[GatewayACL](ctx, c, sitePath(siteID, aclPath+"?type=0"))
}

// GatewayACL reads one rule by ID.
func (c *Client) GatewayACL(ctx context.Context, siteID, id string) (*GatewayACL, error) {
	all, err := c.GatewayACLs(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("gateway ACL %q", id)}
}

// aclWriteBody builds the write shape. The controller rejects the GET shape
// (sourceDirection/destinationDirection) with -1001, so writes always use the
// legacy direction object.
func aclWriteBody(a GatewayACL) map[string]any {
	dir := a.Direction
	if dir == nil {
		dir = &ACLDirection{LANToLAN: true}
	}
	if dir.WANInIDs == nil {
		dir.WANInIDs = []string{}
	}
	if dir.VPNInIDs == nil {
		dir.VPNInIDs = []string{}
	}
	states := a.States
	if states == nil {
		states = &ACLStates{}
	}
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	body := map[string]any{
		"name":            a.Name,
		"type":            0,
		"status":          a.Status,
		"policy":          a.Policy,
		"protocols":       a.Protocols,
		"sourceType":      a.SourceType,
		"sourceIds":       nz(a.SourceIDs),
		"destinationType": a.DestinationType,
		"destinationIds":  nz(a.DestinationIDs),
		"direction":       dir,
		"timeRangeEnable": a.TimeRangeEnable,
		"syslog":          a.Syslog,
		"stateMode":       a.StateMode,
		"states":          states,
	}
	if a.TimeRangeEnable {
		body["timeRangeId"] = a.TimeRangeID
	}
	return body
}

// CreateGatewayACL creates a rule (appended to the end of the table).
// Names must be unique; the ID is resolved by name when needed.
func (c *Client) CreateGatewayACL(ctx context.Context, siteID string, a GatewayACL) (*GatewayACL, error) {
	before, err := c.GatewayACLs(ctx, siteID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, x := range before {
		if x.Name == a.Name {
			return nil, fmt.Errorf("a gateway ACL named %q already exists (id %s); import it instead", a.Name, x.ID)
		}
		seen[x.ID] = true
	}
	var res json.RawMessage
	if err := c.Do(ctx, http.MethodPost, sitePath(siteID, aclPath), aclWriteBody(a), &res); err != nil {
		return nil, err
	}
	if id := idFromResult(res); id != "" {
		return c.GatewayACL(ctx, siteID, id)
	}
	after, err := c.GatewayACLs(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range after {
		if !seen[after[i].ID] && after[i].Name == a.Name {
			return &after[i], nil
		}
	}
	return nil, fmt.Errorf("gateway ACL %q was created but could not be read back", a.Name)
}

// UpdateGatewayACL replaces a rule's settings.
func (c *Client) UpdateGatewayACL(ctx context.Context, siteID, id string, a GatewayACL) error {
	return c.Do(ctx, http.MethodPut, sitePath(siteID, aclPath+"/"+url.PathEscape(id)), aclWriteBody(a), nil)
}

// DeleteGatewayACL deletes a rule.
func (c *Client) DeleteGatewayACL(ctx context.Context, siteID, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, aclPath+"/"+url.PathEscape(id)), nil, nil)
}

// ---------- IP and IP-port groups ----------

// GroupIP is one subnet in a group.
type GroupIP struct {
	IP          string `json:"ip"`
	Mask        int    `json:"mask"`
	Description string `json:"description"`
}

// Group is an IP group (type 0) or IP-port group (type 1).
type Group struct {
	ID       string    `json:"groupId,omitempty"`
	Name     string    `json:"name"`
	Type     int       `json:"type"`
	PortType int       `json:"portType"`
	IPList   []GroupIP `json:"ipList"`
	PortList []string  `json:"portList,omitempty"`
}

const groupsPath = "/setting/profiles/groups"

// Groups lists IP and IP-port groups.
func (c *Client) Groups(ctx context.Context, siteID string) ([]Group, error) {
	return listAll[Group](ctx, c, sitePath(siteID, groupsPath))
}

// Group reads one group by ID.
func (c *Client) Group(ctx context.Context, siteID, id string) (*Group, error) {
	all, err := c.Groups(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, &NotFoundError{What: fmt.Sprintf("group %q", id)}
}

// CreateGroup creates a group and returns it with its ID. The controller does
// not support editing groups in place, so callers must replace them.
func (c *Client) CreateGroup(ctx context.Context, siteID string, g Group) (*Group, error) {
	g.ID = ""
	if g.Type == 0 {
		g.PortList = nil
	}
	var res json.RawMessage
	if err := c.Do(ctx, http.MethodPost, sitePath(siteID, groupsPath), g, &res); err != nil {
		return nil, err
	}
	if id := idFromResult(res); id != "" {
		return c.Group(ctx, siteID, id)
	}
	all, err := c.Groups(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == g.Name && all[i].Type == g.Type {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("group %q was created but could not be read back", g.Name)
}

// DeleteGroup deletes a group. The path includes the group type.
func (c *Client) DeleteGroup(ctx context.Context, siteID string, groupType int, id string) error {
	return c.Do(ctx, http.MethodDelete, sitePath(siteID, fmt.Sprintf("%s/%d/%s", groupsPath, groupType, url.PathEscape(id))), nil, nil)
}

// ---------- helpers ----------

func remarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func deepCopy(m map[string]any) map[string]any {
	var out map[string]any
	_ = remarshal(m, &out)
	return out
}

// idFromResult extracts an object ID from a create response, which may be a
// bare string, {"id": ...}, or {"groupId": ...}.
func idFromResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"id", "groupId"} {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}
