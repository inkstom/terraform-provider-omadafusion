package fake

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// AllProfileID and DisableProfileID are the built-in port profiles.
const (
	AllProfileID     = "prof00000000000000000all"
	DisableProfileID = "prof0000000000000disable"
	SwitchMAC        = "AA-BB-CC-00-00-03"
	WAN1             = "1_0000000000000000000000000000wan1"
)

func (s *Server) seedSwitching() {
	var tagged []any
	var native string
	for id, n := range s.Networks {
		if n["primary"] == true {
			native = id
		} else {
			tagged = append(tagged, id)
		}
	}
	s.Profiles[AllProfileID] = map[string]any{"id": AllProfileID, "name": "All", "flag": 0, "type": 0,
		"nativeNetworkId": native, "tagNetworkIds": tagged, "untagNetworkIds": []any{},
		"spanningTreeEnable": true, "lldpMedEnable": true}
	s.Profiles[DisableProfileID] = map[string]any{"id": DisableProfileID, "name": "Disable", "flag": 0, "type": 1,
		"tagNetworkIds": []any{}, "untagNetworkIds": []any{}, "loopbackDetectEnable": true, "lldpMedEnable": true}
	for p := 1; p <= 8; p++ {
		s.Ports[fmt.Sprintf("%s/%d", SwitchMAC, p)] = map[string]any{"port": p, "name": fmt.Sprintf("Port%d", p),
			"profileId": AllProfileID, "profileName": "All", "profileOverrideEnable": false}
	}
	for _, n := range s.Networks {
		n["interfaceIds"] = []any{"2_lan", "3_lan"}
	}
}

func valuesSortedByID(m map[string]map[string]any) []map[string]any {
	return sortedValues(m)
}

func nameTaken(m map[string]map[string]any, name any, except string) bool {
	for id, o := range m {
		if id != except && o["name"] == name {
			return true
		}
	}
	return false
}

// handleExt serves the endpoints added for networks, switching, NAT,
// routing, SSID lifecycle and ACL ordering. It returns false for unknown
// paths. Behaviour mirrors what a 6.3.14.2 controller did when probed.
func (s *Server) handleExt(w http.ResponseWriter, r *http.Request, path, site string, body map[string]any) bool {
	done := func(result any) bool { ok(w, result); return true }
	fail := func(code int, msg string) bool { reply(w, code, msg, nil); return true }
	switch {
	// ---- LAN networks: create/delete ----
	case path == site+"/setting/lan/networks" && r.Method == http.MethodPost:
		if nameTaken(s.Networks, body["name"], "") {
			return fail(-33002, "The network name already exists.")
		}
		v6, _ := body["lanNetworkIpv6Config"].(map[string]any)
		if v6 == nil || v6["proto"] == nil {
			return fail(-1001, "proto should not be null")
		}
		id := s.nextID()
		body["id"], body["site"], body["origName"], body["primary"] = id, SiteID, body["name"], false
		s.Networks[id] = body
		all := s.Profiles[AllProfileID]
		all["tagNetworkIds"] = append(all["tagNetworkIds"].([]any), id)
		pid := s.nextID()
		s.Profiles[pid] = map[string]any{"id": pid, "name": body["name"], "flag": 1, "type": 2, "nativeNetworkId": id,
			"tagNetworkIds": []any{}, "untagNetworkIds": []any{}, "autoFor": id}
		return done(id)
	case strings.HasPrefix(path, site+"/setting/lan/networks/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, site+"/setting/lan/networks/")
		n, found := s.Networks[id]
		if !found {
			return fail(-33004, "The network does not exist.")
		}
		if n["primary"] == true {
			return fail(-33005, "The default network cannot be deleted.")
		}
		delete(s.Networks, id)
		for pid, p := range s.Profiles {
			if p["autoFor"] == id {
				delete(s.Profiles, pid)
			}
			var keep []any
			for _, t := range p["tagNetworkIds"].([]any) {
				if t != id {
					keep = append(keep, t)
				}
			}
			if keep == nil {
				keep = []any{}
			}
			p["tagNetworkIds"] = keep
		}
		return done(nil)

	// ---- port profiles ----
	case path == site+"/setting/lan/profiles" && r.Method == http.MethodGet:
		return done(paged(valuesSortedByID(s.Profiles)))
	case path == site+"/setting/lan/profiles" && r.Method == http.MethodPost:
		if nameTaken(s.Profiles, body["name"], "") {
			return fail(-33102, "The profile name already exists.")
		}
		id := s.nextID()
		body["id"], body["flag"], body["type"] = id, 2, 2
		if _, has := body["tagNetworkIds"]; !has {
			body["tagNetworkIds"] = []any{}
		}
		s.Profiles[id] = body
		return done(map[string]any{"id": id})
	case strings.HasPrefix(path, site+"/setting/lan/profiles/"):
		id := strings.TrimPrefix(path, site+"/setting/lan/profiles/")
		cur, found := s.Profiles[id]
		if !found {
			return fail(-33101, "The profile does not exist.")
		}
		switch r.Method {
		case http.MethodPatch:
			if _, bad := body["id"]; bad {
				return fail(-1001, "Invalid request parameters.")
			}
			body["id"], body["flag"], body["type"] = id, cur["flag"], cur["type"]
			s.Profiles[id] = body
		case http.MethodDelete:
			for _, p := range s.Ports {
				if p["profileId"] == id {
					return fail(-33103, "The profile is in use.")
				}
			}
			delete(s.Profiles, id)
		default:
			return false
		}
		return done(nil)

	// ---- switch ports ----
	case strings.HasPrefix(path, site+"/switches/") && strings.HasSuffix(path, "/ports") && r.Method == http.MethodGet:
		mac := strings.TrimSuffix(strings.TrimPrefix(path, site+"/switches/"), "/ports")
		var out []map[string]any
		for k, p := range s.Ports {
			if strings.HasPrefix(k, mac+"/") {
				out = append(out, p)
			}
		}
		if out == nil {
			return fail(-39002, "The device does not exist.")
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["port"].(int) < out[j]["port"].(int) })
		return done(out)
	case strings.HasPrefix(path, site+"/switches/") && strings.Contains(path, "/ports/") && r.Method == http.MethodPatch:
		key := strings.Replace(strings.TrimPrefix(path, site+"/switches/"), "/ports/", "/", 1)
		p, found := s.Ports[key]
		if !found {
			return fail(-39002, "The device does not exist.")
		}
		prof, okProf := s.Profiles[fmt.Sprint(body["profileId"])]
		if !okProf {
			return fail(-1001, "profileId is invalid")
		}
		p["name"], p["profileId"], p["profileName"] = body["name"], body["profileId"], prof["name"]
		return done(nil)

	// ---- WAN ports ----
	case path == site+"/setting/wan/networks" && r.Method == http.MethodGet:
		return done(map[string]any{"osgPortInfo": map[string]any{"wanLanPortSettings": []any{
			map[string]any{"port": 1, "portUuid": WAN1, "portName": "WAN1", "type": 3, "status": 1, "online": 1, "ipv4Addr": "203.0.113.10"},
			map[string]any{"port": 2, "portUuid": "2_lan", "portName": "WAN/LAN2", "type": 1, "status": 0},
		}}})

	// ---- port forwarding ----
	case path == site+"/setting/transmission/portForwardings" && r.Method == http.MethodGet:
		return done(paged(valuesSortedByID(s.Forwards)))
	case path == site+"/setting/transmission/portForwardings" && r.Method == http.MethodPost:
		if body["dMZ"] == nil || body["from"] == nil {
			return fail(-1001, "PortForwarding parameter [dMZ] should not be null")
		}
		id := s.nextID()
		body["id"] = id
		s.Forwards[id] = body
		return done(nil) // no ID in the create response
	case strings.HasPrefix(path, site+"/setting/transmission/portForwardings/"):
		id := strings.TrimPrefix(path, site+"/setting/transmission/portForwardings/")
		if _, found := s.Forwards[id]; !found {
			return fail(-33500, "The rule does not exist.")
		}
		switch r.Method {
		case http.MethodPut:
			body["id"] = id
			s.Forwards[id] = body
		case http.MethodDelete:
			delete(s.Forwards, id)
		default:
			return fail(-1600, "Unsupported request path.")
		}
		return done(nil)

	// ---- static routes (GET omits interfaceId, like the real controller) ----
	case path == site+"/setting/transmission/staticRoutings" && r.Method == http.MethodGet:
		var out []map[string]any
		for _, rt := range valuesSortedByID(s.Routes) {
			cp := map[string]any{}
			for k, v := range rt {
				if k != "interfaceId" {
					cp[k] = v
				}
			}
			out = append(out, cp)
		}
		return done(paged(out))
	case path == site+"/setting/transmission/staticRoutings" && r.Method == http.MethodPost:
		id := s.nextID()
		body["id"] = id
		s.Routes[id] = body
		return done(nil)
	case strings.HasPrefix(path, site+"/setting/transmission/staticRoutings/"):
		id := strings.TrimPrefix(path, site+"/setting/transmission/staticRoutings/")
		if _, found := s.Routes[id]; !found {
			return fail(-33600, "The route does not exist.")
		}
		switch r.Method {
		case http.MethodPut:
			body["id"] = id
			s.Routes[id] = body
		case http.MethodDelete:
			delete(s.Routes, id)
		default:
			return fail(-1600, "Unsupported request path.")
		}
		return done(nil)

	// ---- SSID create/delete ----
	case path == site+"/setting/wlans/"+WLANID+"/ssids" && r.Method == http.MethodPost:
		if body["rateLimit"] == nil {
			return fail(-1001, "rateLimit should not be null")
		}
		id := s.nextID()
		body["id"], body["wlanId"], body["site"], body["idInt"] = id, WLANID, SiteID, len(s.SSIDs)+100
		body["featureDescription"] = []any{}
		s.SSIDs[id] = body
		return done(map[string]any{"ssidId": id})
	case strings.HasPrefix(path, site+"/setting/wlans/"+WLANID+"/ssids/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, site+"/setting/wlans/"+WLANID+"/ssids/")
		if _, found := s.SSIDs[id]; !found {
			return fail(-33000, "SSID does not exist.")
		}
		delete(s.SSIDs, id)
		return done(nil)

	// ---- ACL order ----
	case path == site+"/cmd/acls/modifyIndex" && r.Method == http.MethodPost:
		idx, _ := body["indexes"].(map[string]any)
		if len(idx) != len(s.ACLs) {
			return fail(-1001, "indexes must cover every rule")
		}
		sort.SliceStable(s.ACLs, func(i, j int) bool {
			a, _ := idx[fmt.Sprint(s.ACLs[i]["id"])].(float64)
			b, _ := idx[fmt.Sprint(s.ACLs[j]["id"])].(float64)
			return a < b
		})
		return done(nil)
	}
	return false
}
