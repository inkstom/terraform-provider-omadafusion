// Package fake is an in-memory stand-in for an Omada controller's v2 web
// API, used by unit and acceptance tests. It mimics the request and response
// shapes, envelope, CSRF/session handling and the validation quirks observed
// on a real Fusion integrated controller (6.3.x). It is not a full emulator.
package fake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

const (
	ControllerID = "0123456789abcdef0123456789abcdef"
	SiteID       = "site000000000000000000001"
	SiteName     = "Default"
	WLANID       = "wlan000000000000000000001"
	Username     = "admin"
	Password     = "test-password"
	csrfToken    = "csrf-test-token"
	sessionName  = "TPOMADA_SESSIONID"
)

// Server is a fake controller.
type Server struct {
	*httptest.Server

	mu         sync.Mutex
	seq        int
	sessions   map[string]bool
	Logins     int
	Networks   map[string]map[string]any
	SSIDs      map[string]map[string]any
	TimeRanges map[string]map[string]any
	DHCP       map[string]map[string]any // keyed by MAC
	ACLs       []map[string]any
	Groups     map[string]map[string]any
	Profiles   map[string]map[string]any
	Ports      map[string]map[string]any // "<mac>/<port>"
	Forwards   map[string]map[string]any
	Routes     map[string]map[string]any
	// ExpireSessions makes the next authenticated call fail as expired.
	ExpireSessions bool
}

// New starts a TLS fake controller seeded with networks and SSIDs.
func New() *Server {
	s := &Server{
		sessions:   map[string]bool{},
		Networks:   map[string]map[string]any{},
		SSIDs:      map[string]map[string]any{},
		TimeRanges: map[string]map[string]any{},
		DHCP:       map[string]map[string]any{},
		Groups:     map[string]map[string]any{},
		Profiles:   map[string]map[string]any{},
		Ports:      map[string]map[string]any{},
		Forwards:   map[string]map[string]any{},
		Routes:     map[string]map[string]any{},
	}
	s.seed()
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	return s
}

func (s *Server) nextID() string {
	s.seq++
	return fmt.Sprintf("%024x", 0xa000+s.seq)
}

func (s *Server) seed() {
	add := func(name string, vlan int, subnet string, def bool) {
		id := s.nextID()
		n := map[string]any{
			"id": id, "name": name, "origName": name, "vlan": vlan, "gatewaySubnet": subnet,
			"purpose": "interface", "isolation": false, "site": SiteID,
			"dhcpSettings": map[string]any{"enable": true, "dhcpns": "auto", "leasetime": 120,
				"ipaddrStart": strings.TrimSuffix(subnet, ".1/24") + ".50", "ipaddrEnd": strings.TrimSuffix(subnet, ".1/24") + ".199"},
			"lanNetworkIpv6Config": map[string]any{"enable": 0},
		}
		if def {
			n["name"] = name + "(Default)"
			n["primary"] = true
		}
		s.Networks[id] = n
	}
	add("Management", 1, "192.168.10.1/24", true)
	defer s.seedSwitching()
	add("Staff", 20, "192.168.20.1/24", false)
	add("Guest", 40, "192.168.40.1/24", false)
	for _, name := range []string{"Staff WiFi", "Guest WiFi"} {
		id := s.nextID()
		s.SSIDs[id] = map[string]any{
			"id": id, "idInt": len(s.SSIDs) + 1, "name": name, "wlanId": WLANID, "site": SiteID,
			"band": 3, "security": 3, "broadcast": true, "vlanEnable": true,
			"guestNetEnable": name == "Guest WiFi", "wlanScheduleEnable": false,
			"pskSetting":         map[string]any{"securityKey": "not-a-real-key"},
			"featureDescription": []any{},
		}
	}
}

// NetworkID returns the ID of a seeded network by original name.
func (s *Server) NetworkID(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, n := range s.Networks {
		if n["origName"] == name {
			return id
		}
	}
	return ""
}

// SSIDID returns the ID of a seeded SSID by name.
func (s *Server) SSIDID(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, n := range s.SSIDs {
		if n["name"] == name {
			return id
		}
	}
	return ""
}

func reply(w http.ResponseWriter, code int, msg string, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": code, "msg": msg, "result": result})
}

func ok(w http.ResponseWriter, result any) { reply(w, 0, "Success.", result) }

func paged(items []map[string]any) map[string]any {
	return map[string]any{"totalRows": len(items), "currentPage": 1, "currentSize": 100, "data": items}
}

func sortedValues(m map[string]map[string]any) []map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.URL.Path == "/api/info" {
		ok(w, map[string]any{"controllerVer": "6.3.14.2", "apiVer": "3", "configured": true, "type": 19,
			"omadacId": ControllerID, "omadacCategory": "advanced"})
		return
	}
	prefix := "/" + ControllerID + "/api/v2"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	if path == "/login" && r.Method == http.MethodPost {
		if body["username"] != Username || body["password"] != Password {
			reply(w, -30109, "Invalid username or password.", nil)
			return
		}
		s.Logins++
		sid := fmt.Sprintf("sess-%d", s.Logins)
		s.sessions[sid] = true
		http.SetCookie(w, &http.Cookie{Name: sessionName, Value: sid, Path: "/"})
		ok(w, map[string]any{"token": csrfToken})
		return
	}

	c, err := r.Cookie(sessionName)
	if err != nil || !s.sessions[c.Value] || r.Header.Get("Csrf-Token") != csrfToken {
		reply(w, -1200, "Session timed out.", nil)
		return
	}
	if s.ExpireSessions {
		s.ExpireSessions = false
		s.sessions = map[string]bool{}
		reply(w, -1200, "Session timed out.", nil)
		return
	}

	site := "/sites/" + SiteID
	switch {
	case path == "/sites" && r.Method == http.MethodGet:
		ok(w, paged([]map[string]any{{"id": SiteID, "name": SiteName, "type": 0}}))

	case path == site+"/devices" && r.Method == http.MethodGet:
		ok(w, []map[string]any{
			{"mac": "AA-BB-CC-00-00-01", "name": "GW-01", "type": "gateway", "model": "Fusion 2.5G", "ip": "192.168.10.1", "status": 14, "statusCategory": 1, "firmwareVersion": "1.0.10"},
			{"mac": "AA-BB-CC-00-00-02", "name": "AP-01", "type": "ap", "model": "EAP650", "ip": "192.168.10.4", "status": 14, "statusCategory": 1, "firmwareVersion": "1.3.30"},
		})

	case path == site+"/setting/lan/networks" && r.Method == http.MethodGet:
		ok(w, paged(sortedValues(s.Networks)))
	case strings.HasPrefix(path, site+"/setting/lan/networks/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, site+"/setting/lan/networks/")
		if _, found := s.Networks[id]; !found {
			reply(w, -33004, "The network does not exist.", nil)
			return
		}
		if d, _ := body["dhcpSettings"].(map[string]any); d != nil {
			if v, has := d["sndDns"]; has && v == "" {
				reply(w, -1001, "Invalid DHCP Name Server IP2", nil)
				return
			}
		}
		v6, _ := body["lanNetworkIpv6Config"].(map[string]any)
		if v6 == nil || v6["proto"] == nil {
			reply(w, -1001, "proto should not be null", nil)
			return
		}
		if body["primary"] != true {
			body["origName"] = body["name"]
		}
		s.Networks[id] = body

	case path == site+"/setting/wlans" && r.Method == http.MethodGet:
		ok(w, paged([]map[string]any{{"id": WLANID, "name": "Default", "primary": true}}))
	case path == site+"/setting/wlans/"+WLANID+"/ssids" && r.Method == http.MethodGet:
		ok(w, paged(sortedValues(s.SSIDs)))
	case strings.HasPrefix(path, site+"/setting/wlans/"+WLANID+"/ssids/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, site+"/setting/wlans/"+WLANID+"/ssids/")
		cur, found := s.SSIDs[id]
		if !found {
			reply(w, -33000, "SSID does not exist.", nil)
			return
		}
		if body["pskSetting"] == nil || body["name"] == nil || body["name"] == "" {
			reply(w, -1001, "Invalid request parameters.", nil)
			return
		}
		if body["wlanScheduleEnable"] == true {
			if _, has := s.TimeRanges[fmt.Sprint(body["scheduleId"])]; !has {
				reply(w, -1001, "scheduleId does not exist", nil)
				return
			}
		} else {
			delete(body, "scheduleId")
			delete(body, "action")
		}
		body["featureDescription"] = []any{}
		body["id"], body["wlanId"] = id, WLANID
		for k, v := range cur {
			if _, has := body[k]; !has && (k == "idInt" || k == "site") {
				body[k] = v
			}
		}
		s.SSIDs[id] = body

	case path == site+"/setting/profiles/timeranges" && r.Method == http.MethodGet:
		ok(w, paged(sortedValues(s.TimeRanges)))
	case path == site+"/setting/profiles/timeranges" && r.Method == http.MethodPost:
		for _, t := range s.TimeRanges {
			if t["name"] == body["name"] {
				reply(w, -33605, "The name already exists.", nil)
				return
			}
		}
		id := s.nextID()
		body["id"], body["site"], body["resource"] = id, SiteID, 0
		normalizeDayMode(body)
		s.TimeRanges[id] = body
		ok(w, map[string]any{"profileId": id}) // as 6.3.14.2 replies
	case strings.HasPrefix(path, site+"/setting/profiles/timeranges/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, site+"/setting/profiles/timeranges/")
		if _, found := s.TimeRanges[id]; !found {
			reply(w, -33607, "The time range does not exist.", nil)
			return
		}
		body["id"], body["site"], body["resource"] = id, SiteID, 0
		normalizeDayMode(body)
		s.TimeRanges[id] = body
	case strings.HasPrefix(path, site+"/setting/profiles/timeranges/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, site+"/setting/profiles/timeranges/")
		for _, ss := range s.SSIDs {
			if ss["scheduleId"] == id && ss["wlanScheduleEnable"] == true {
				reply(w, -33606, "The time range is in use.", nil)
				return
			}
		}
		delete(s.TimeRanges, id)

	case path == site+"/setting/service/dhcp" && r.Method == http.MethodGet:
		items := sortedValues(s.DHCP)
		for _, it := range items {
			for id, n := range s.Networks {
				if it["netId"] == id {
					it["netName"] = n["origName"]
				}
			}
		}
		ok(w, map[string]any{"currentPage": 1, "currentSize": 100, "data": items})
	case path == site+"/setting/service/dhcp" && r.Method == http.MethodPost:
		mac, _ := body["mac"].(string)
		if _, exists := s.DHCP[mac]; exists {
			reply(w, -33701, "The MAC address already exists.", nil)
			return
		}
		if _, okNet := s.Networks[fmt.Sprint(body["netId"])]; !okNet {
			reply(w, -1001, "netId is invalid", nil)
			return
		}
		body["id"] = s.nextID()
		s.DHCP[mac] = body
	case strings.HasPrefix(path, site+"/setting/service/dhcp/"):
		mac := strings.TrimPrefix(path, site+"/setting/service/dhcp/")
		cur, exists := s.DHCP[mac]
		if !exists {
			reply(w, -33702, "The reservation does not exist.", nil)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body["id"] = cur["id"]
			s.DHCP[mac] = body
		case http.MethodDelete:
			delete(s.DHCP, mac)
		default:
			reply(w, -1600, "Unsupported request path.", nil)
			return
		}

	case path == site+"/setting/firewall/acls" && r.Method == http.MethodGet:
		if r.URL.Query().Get("type") == "" {
			reply(w, -1001, "type should not be null", nil)
			return
		}
		out := []map[string]any{}
		for i, a := range s.ACLs {
			a["index"] = i + 1
			out = append(out, a)
		}
		res := paged(out)
		res["aclDisable"] = true
		ok(w, res)
	case path == site+"/setting/firewall/acls" && r.Method == http.MethodPost:
		if bad := aclInvalid(body); bad != "" {
			reply(w, -1001, bad, nil)
			return
		}
		body["id"] = s.nextID()
		s.ACLs = append(s.ACLs, body)
	case strings.HasPrefix(path, site+"/setting/firewall/acls/"):
		id := strings.TrimPrefix(path, site+"/setting/firewall/acls/")
		idx := -1
		for i, a := range s.ACLs {
			if a["id"] == id {
				idx = i
			}
		}
		if idx < 0 {
			reply(w, -33300, "The ACL does not exist.", nil)
			return
		}
		switch r.Method {
		case http.MethodPut:
			if bad := aclInvalid(body); bad != "" {
				reply(w, -1001, bad, nil)
				return
			}
			body["id"] = id
			s.ACLs[idx] = body
		case http.MethodDelete:
			s.ACLs = append(s.ACLs[:idx], s.ACLs[idx+1:]...)
		default:
			reply(w, -1600, "Unsupported request path.", nil)
			return
		}

	case path == site+"/setting/profiles/groups" && r.Method == http.MethodGet:
		ok(w, paged(sortedValues(s.Groups)))
	case path == site+"/setting/profiles/groups" && r.Method == http.MethodPost:
		id := s.nextID()
		body["groupId"] = id
		s.Groups[id] = body
		ok(w, nil)
	case strings.HasPrefix(path, site+"/setting/profiles/groups/") && r.Method == http.MethodDelete:
		parts := strings.Split(strings.TrimPrefix(path, site+"/setting/profiles/groups/"), "/")
		if len(parts) != 2 {
			reply(w, -1600, "Unsupported request path.", nil)
			return
		}
		g, exists := s.Groups[parts[1]]
		if !exists || fmt.Sprint(g["type"]) != parts[0] {
			reply(w, -33400, "The group does not exist.", nil)
			return
		}
		delete(s.Groups, parts[1])
	case strings.HasPrefix(path, site+"/setting/profiles/groups/") && r.Method == http.MethodPatch:
		parts := strings.Split(strings.TrimPrefix(path, site+"/setting/profiles/groups/"), "/")
		if len(parts) != 2 {
			reply(w, -1600, "Unsupported request path.", nil)
			return
		}
		g, exists := s.Groups[parts[1]]
		if !exists || fmt.Sprint(g["type"]) != parts[0] {
			reply(w, -33400, "The group does not exist.", nil)
			return
		}
		body["groupId"] = parts[1]
		s.Groups[parts[1]] = body
	case strings.HasPrefix(path, site+"/setting/profiles/groups/"):
		reply(w, -1600, "Unsupported request path.", nil)
		return

	default:
		if !s.handleExt(w, r, path, site, body) {
			reply(w, -1600, "Unsupported request path.", nil)
		}
		return
	}
	if !wroteBody(w) {
		ok(w, nil)
	}
}

// normalizeDayMode mirrors the controller dropping day flags that do not
// belong to the Weekday (1) or Weekend (2) modes.
func normalizeDayMode(b map[string]any) {
	mode, _ := b["dayMode"].(float64)
	switch int(mode) {
	case 1:
		b["daySat"], b["daySun"] = false, false
	case 2:
		for _, d := range []string{"dayMon", "dayTue", "dayWed", "dayThu", "dayFri"} {
			b[d] = false
		}
	}
}

// aclInvalid mirrors the controller rejecting the GET shape on write.
func aclInvalid(b map[string]any) string {
	if _, has := b["sourceDirection"]; has {
		return "Invalid request parameters."
	}
	if _, has := b["direction"]; !has {
		return "direction should not be null"
	}
	if p, _ := b["protocols"].([]any); len(p) == 0 {
		return "protocols should not be empty"
	}
	return ""
}

// wroteBody reports whether a handler already sent a response. The handlers
// above only call reply/ok on error or GET paths, so writes fall through.
func wroteBody(w http.ResponseWriter) bool {
	if rw, isRec := w.(interface{ Written() bool }); isRec {
		return rw.Written()
	}
	return w.Header().Get("Content-Type") != ""
}
