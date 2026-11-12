package fake

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// OpsState holds the objects behind the VPN, DDNS, site-settings, backup,
// account and device-settings endpoints. Behaviour mirrors a 6.3.14.2
// controller as probed: PUT edits for WireGuard/DDNS, a composite site
// settings object edited by PATCH per section, an auto-backup task that
// rejects echoed read-only fields, and account deletes that need
// forceDelete.
type OpsState struct {
	WireGuard  map[string]map[string]any
	Peers      map[string]map[string]any
	DDNS       map[string]map[string]any
	Settings   map[string]any
	Services   map[string]map[string]any
	AutoBackup map[string]any
	Users      map[string]map[string]any
	Passwords  map[string]string
	Devices    map[string]map[string]any // detail by MAC
	Reboots    map[string]map[string]any
}

func (s *Server) ops() *OpsState {
	if s.Ops != nil {
		return s.Ops
	}
	s.Ops = &OpsState{
		WireGuard: map[string]map[string]any{}, Peers: map[string]map[string]any{}, DDNS: map[string]map[string]any{},
		Settings: map[string]any{
			"site": map[string]any{"key": SiteID, "name": SiteName, "region": "United States", "timeZone": "America/New_York",
				"scenario": "Office", "ntpEnable": false, "ntpServers": []any{}, "primary": true},
			"led": map[string]any{"enable": true}, "autoUpgrade": map[string]any{"enable": false},
			"lldp": map[string]any{"enable": true}, "mesh": map[string]any{"meshEnable": true, "autoFailoverEnable": true},
			"bandSteering": map[string]any{"enable": false, "connectionThreshold": 30},
			"roaming":      map[string]any{"fastRoamingEnable": false}, "alert": map[string]any{"enable": false, "delay": 60},
			"remoteLog":     map[string]any{"enable": false, "port": 514, "moreClientLog": false},
			"deviceAccount": map[string]any{"username": "admin", "password": "not-a-real-password"},
		},
		Services: map[string]map[string]any{
			"ssh":  {"sshEnable": false, "sshServerPort": 22, "layer3Access": false},
			"upnp": {"enable": false}, "mdns": {"enable": false},
		},
		AutoBackup: map[string]any{"enable": false, "occurrence": map[string]any{"timingType": 3, "hour": 12, "minute": 0, "dayOfMonth": 1},
			"maxNumberOfFile": 7, "retention": 30, "type": "FusionNetworkv1", "nowStatus": -2, "savingPath": "/data/autobackup"},
		Users:     map[string]map[string]any{},
		Passwords: map[string]string{},
		Devices:   map[string]map[string]any{},
		Reboots:   map[string]map[string]any{},
	}
	s.Ops.Devices["AA-BB-CC-00-00-02"] = map[string]any{"mac": "AA-BB-CC-00-00-02", "name": "AP-01", "type": "ap", "ledSetting": 2,
		"radioSetting2g": map[string]any{"radioEnable": true, "channelWidth": "4", "channel": "0", "txPower": 20, "txPowerLevel": 4},
		"radioSetting5g": map[string]any{"radioEnable": true, "channelWidth": "6", "channel": "0", "txPower": 20, "txPowerLevel": 4}}
	s.Ops.Devices["AA-BB-CC-00-00-01"] = map[string]any{"mac": "AA-BB-CC-00-00-01", "name": "GW-01", "type": "gateway", "ledSetting": 2}
	return s.Ops
}

func strongPassword(p string) bool {
	var d, l, o bool
	for _, c := range p {
		switch {
		case unicode.IsDigit(c):
			d = true
		case unicode.IsLetter(c):
			l = true
		default:
			o = true
		}
	}
	return len(p) >= 8 && d && l && o
}

func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

// handleOps serves the endpoints added for VPN, DDNS, site settings, auto
// backup, accounts and device settings. It returns false for unknown paths.
func (s *Server) handleOps(w http.ResponseWriter, r *http.Request, path, site string, body map[string]any) bool {
	o := s.ops()
	done := func(result any) bool { ok(w, result); return true }
	fail := func(code int, msg string) bool { reply(w, code, msg, nil); return true }
	tail := func(prefix string) string { return strings.TrimPrefix(path, prefix) }
	m := r.Method

	switch {
	// ---- WireGuard ----
	case path == site+"/setting/wireguard/key" && m == http.MethodGet:
		s.seq++
		return done(map[string]any{"privateKey": fmt.Sprintf("priv-%d=", s.seq), "publicKey": fmt.Sprintf("pub-%d=", s.seq)})
	case path == site+"/setting/wireguard/peer" && m == http.MethodGet:
		return done(paged(sortedValues(o.Peers)))
	case path == site+"/setting/wireguard/peer" && m == http.MethodPost:
		if o.WireGuard[fmt.Sprint(body["interfaceId"])] == nil {
			return fail(-1001, "Invalid request parameters.")
		}
		id := s.nextID()
		body["id"] = id
		if body["endPointPort"] == nil {
			delete(body, "endPointPort")
		}
		o.Peers[id] = body
		return done(id)
	case strings.HasPrefix(path, site+"/setting/wireguard/peer/"):
		id := tail(site + "/setting/wireguard/peer/")
		switch m {
		case http.MethodPut:
			if o.Peers[id] == nil {
				return fail(-1, "General error.")
			}
			body["id"] = id
			if body["endPointPort"] == nil {
				delete(body, "endPointPort")
			}
			o.Peers[id] = body
			return done(nil)
		case http.MethodDelete:
			delete(o.Peers, id)
			return done(nil)
		}
		return fail(-1600, "Unsupported request path.")
	case path == site+"/setting/wireguard" && m == http.MethodGet:
		return done(paged(sortedValues(o.WireGuard)))
	case path == site+"/setting/wireguard" && m == http.MethodPost:
		if nameTaken(o.WireGuard, body["name"], "") {
			return fail(-1001, "The name already exists.")
		}
		id := s.nextID()
		body["id"] = id
		body["publicKey"] = strings.Replace(fmt.Sprint(body["privateKey"]), "priv", "pub", 1)
		o.WireGuard[id] = body
		return done(id)
	case strings.HasPrefix(path, site+"/setting/wireguard/"):
		id := tail(site + "/setting/wireguard/")
		switch m {
		case http.MethodPut:
			if o.WireGuard[id] == nil {
				return fail(-1, "General error.")
			}
			body["id"] = id
			body["publicKey"] = strings.Replace(fmt.Sprint(body["privateKey"]), "priv", "pub", 1)
			o.WireGuard[id] = body
			return done(nil)
		case http.MethodDelete:
			delete(o.WireGuard, id)
			for pid, p := range o.Peers {
				if p["interfaceId"] == id {
					delete(o.Peers, pid)
				}
			}
			return done(nil)
		}
		return fail(-1600, "Unsupported request path.")

	// ---- Dynamic DNS ----
	case path == site+"/setting/service/ddns" && m == http.MethodGet:
		return done(paged(sortedValues(o.DDNS)))
	case path == site+"/setting/service/ddns" && m == http.MethodPost:
		id := s.nextID()
		body["id"] = id
		o.DDNS[id] = body
		return done(id)
	case strings.HasPrefix(path, site+"/setting/service/ddns/"):
		id := tail(site + "/setting/service/ddns/")
		switch m {
		case http.MethodPut:
			body["id"] = id
			o.DDNS[id] = body
			return done(nil)
		case http.MethodDelete:
			delete(o.DDNS, id)
			return done(nil)
		}
		return fail(-1600, "Unsupported request path.")

	// ---- Site settings ----
	case path == site+"/setting" && m == http.MethodGet:
		return done(o.Settings)
	case path == site+"/setting" && m == http.MethodPatch:
		for sec, v := range body {
			vm, _ := v.(map[string]any)
			if sec == "site" {
				if vm["name"] == nil || vm["timeZone"] == nil {
					return fail(-1001, "Invalid request parameters.")
				}
				var addrs []any
				for _, a := range anyList(vm["ntpServers"]) {
					if str, ok := a.(string); ok {
						addrs = append(addrs, map[string]any{"address": str})
					} else {
						addrs = append(addrs, a)
					}
				}
				vm["ntpServers"] = addrs
			}
			if sec == "remoteLog" && vm["host"] == "" {
				return fail(-1001, "Invalid request parameters.")
			}
			cur, _ := o.Settings[sec].(map[string]any)
			if cur == nil {
				cur = map[string]any{}
			}
			mergeInto(cur, vm)
			o.Settings[sec] = cur
		}
		return done(nil)
	case strings.HasPrefix(path, site+"/setting/") && o.Services[tail(site+"/setting/")] != nil:
		name := tail(site + "/setting/")
		switch m {
		case http.MethodGet:
			return done(o.Services[name])
		case http.MethodPut:
			o.Services[name] = body
			return done(nil)
		}
		return fail(-1600, "Unsupported request path.")

	// ---- Auto backup ----
	case path == "/autoBackup/autoBackupTask" && m == http.MethodGet:
		return done(o.AutoBackup)
	case path == "/autoBackup/autoBackupTask" && m == http.MethodPut:
		if _, bad := body["storageType"]; bad {
			return fail(-1001, "Invalid request parameters.")
		}
		delete(body, "nowStatus")
		if body["enable"] != true {
			// The controller ignores schedule changes while disabled.
			body["occurrence"], body["maxNumberOfFile"] = o.AutoBackup["occurrence"], o.AutoBackup["maxNumberOfFile"]
			delete(body, "storageType")
		} else {
			body["storageType"] = 0
		}
		body["nowStatus"] = -2
		o.AutoBackup = body
		return done(nil)

	// ---- Accounts ----
	case path == "/account/users" && m == http.MethodGet:
		return done(paged(sortedValues(o.Users)))
	case path == "/account/users" && m == http.MethodPost:
		if !strongPassword(fmt.Sprint(body["password"])) {
			return fail(-30105, "Invalid password.")
		}
		id := s.nextID()
		o.Passwords[id] = fmt.Sprint(body["password"])
		o.Users[id] = s.userFromBody(id, body)
		return done(map[string]any{"userId": id})
	case strings.HasPrefix(path, "/account/users/") && m == http.MethodPatch:
		id := tail("/account/users/")
		if o.Users[id] == nil {
			return fail(-1, "General error.")
		}
		if body["name"] == nil || body["type"] == nil {
			return fail(-1001, "must not be null\nmust not be null\n")
		}
		if p, ok := body["password"]; ok {
			if !strongPassword(fmt.Sprint(p)) {
				return fail(-30105, "Invalid password.")
			}
			o.Passwords[id] = fmt.Sprint(p)
		}
		o.Users[id] = s.userFromBody(id, body)
		return done(nil)
	case strings.HasPrefix(path, "/users/") && m == http.MethodDelete:
		if body["forceDelete"] != true {
			return fail(-1, "General error.")
		}
		delete(o.Users, tail("/users/"))
		return done(nil)

	// ---- Reboot schedules (create returns no ID; edit is PUT) ----
	case path == site+"/setting/service/rebootSchedules" && m == http.MethodGet:
		return done(paged(sortedValues(o.Reboots)))
	case path == site+"/setting/service/rebootSchedules" && m == http.MethodPost:
		if nameTaken(o.Reboots, body["name"], "") {
			return fail(-1001, "The name already exists.")
		}
		id := s.nextID()
		body["id"] = id
		o.Reboots[id] = body
		return done(nil)
	case strings.HasPrefix(path, site+"/setting/service/rebootSchedules/"):
		id := tail(site + "/setting/service/rebootSchedules/")
		switch m {
		case http.MethodPut:
			body["id"] = id
			o.Reboots[id] = body
			return done(nil)
		case http.MethodDelete:
			delete(o.Reboots, id)
			return done(nil)
		}
		return fail(-1600, "Unsupported request path.")

	// ---- Device settings ----
	case strings.HasPrefix(path, site+"/eaps/") || strings.HasPrefix(path, site+"/gateways/"):
		mac := path[strings.LastIndex(path, "/")+1:]
		d := o.Devices[mac]
		if d == nil {
			return fail(-39050, "The device does not exist.")
		}
		switch m {
		case http.MethodGet:
			return done(d)
		case http.MethodPatch:
			mergeInto(d, body)
			return done(d)
		}
		return fail(-1600, "Unsupported request path.")
	}
	return false
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

func (s *Server) userFromBody(id string, body map[string]any) map[string]any {
	roles := map[string]string{"super_admin_id": "Super Admin", "admin_id": "Admin", "viewer_id": "Viewer"}
	priv, _ := body["privilege"].(map[string]any)
	all, _ := priv["all"].(bool)
	sites := []any{}
	if all {
		sites = append(sites, map[string]any{"name": SiteName, "key": SiteID})
	} else {
		for _, sid := range anyList(priv["sites"]) {
			if sid == SiteID {
				sites = append(sites, map[string]any{"name": SiteName, "key": SiteID})
			}
		}
	}
	return map[string]any{"id": id, "name": body["name"], "type": body["type"], "roleId": body["roleId"],
		"roleName": roles[fmt.Sprint(body["roleId"])], "alert": body["alert"] == true,
		"privilege": map[string]any{"all": all, "sites": sites}}
}

// UserPassword returns the stored password of a fake account.
func (s *Server) UserPassword(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, u := range s.ops().Users {
		if u["name"] == name {
			return s.ops().Passwords[id]
		}
	}
	return ""
}
