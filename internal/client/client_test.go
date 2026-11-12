package client_test

import (
	"context"
	"testing"

	"github.com/inkstom/terraform-provider-omadafusion/internal/client"
	"github.com/inkstom/terraform-provider-omadafusion/internal/fake"
)

func newClient(t *testing.T, srv *fake.Server) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{URL: srv.URL, Username: fake.Username, Password: fake.Password, Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoginAndReloginAfterExpiry(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	c := newClient(t, srv)
	ctx := context.Background()

	site, err := c.SiteByName(ctx, fake.SiteName)
	if err != nil {
		t.Fatalf("site lookup: %v", err)
	}
	if site.ID != fake.SiteID {
		t.Fatalf("site ID = %q", site.ID)
	}
	srv.ExpireSessions = true
	if _, err := c.Devices(ctx, site.ID); err != nil {
		t.Fatalf("expected transparent re-login, got %v", err)
	}
	if srv.Logins != 2 {
		t.Fatalf("logins = %d, want 2", srv.Logins)
	}
}

func TestBadCredentials(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	c, _ := client.New(client.Config{URL: srv.URL, Username: "admin", Password: "wrong", Insecure: true})
	if _, err := c.Sites(context.Background()); err == nil {
		t.Fatal("expected login error")
	}
}

func TestTLSVerificationIsOnByDefault(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	c, _ := client.New(client.Config{URL: srv.URL, Username: fake.Username, Password: fake.Password})
	if _, err := c.Info(context.Background()); err == nil {
		t.Fatal("expected a certificate error without insecure=true")
	}
}

func TestLANDNSSendsIPv6Proto(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	c := newClient(t, srv)
	ctx := context.Background()
	id := srv.NetworkID("Staff")
	if err := c.SetLANNetworkDHCPDNS(ctx, fake.SiteID, id, "manual", "1.1.1.1", "1.0.0.1"); err != nil {
		t.Fatalf("set DNS: %v", err)
	}
	n, err := c.LANNetwork(ctx, fake.SiteID, id)
	if err != nil {
		t.Fatal(err)
	}
	if n.DHCPSettings.DNSMode != "manual" || n.DHCPSettings.PrimaryDNS != "1.1.1.1" || n.DHCPSettings.SecondaryDN != "1.0.0.1" {
		t.Fatalf("unexpected DHCP settings: %+v", n.DHCPSettings)
	}
	if n.VLAN != 20 || n.GatewaySubnet != "192.168.20.1/24" {
		t.Fatalf("unrelated fields changed: vlan=%d subnet=%s", n.VLAN, n.GatewaySubnet)
	}
}

func TestACLCreateUsesLegacyDirectionAndResolvesID(t *testing.T) {
	srv := fake.New()
	defer srv.Close()
	c := newClient(t, srv)
	ctx := context.Background()
	staff, mgmt := srv.NetworkID("Staff"), srv.NetworkID("Management")
	a, err := c.CreateGatewayACL(ctx, fake.SiteID, client.GatewayACL{
		Name: "Deny Staff to Management", Status: true, Policy: 0, Protocols: []int{256},
		SourceIDs: []string{staff}, DestinationIDs: []string{mgmt}, StateMode: 1,
		States: &client.ACLStates{New: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ID == "" || a.Index != 1 {
		t.Fatalf("unexpected readback: %+v", a)
	}
	if _, err := c.CreateGatewayACL(ctx, fake.SiteID, client.GatewayACL{Name: a.Name, Protocols: []int{6}}); err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if err := c.DeleteGatewayACL(ctx, fake.SiteID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GatewayACL(ctx, fake.SiteID, a.ID); !client.IsNotFound(err) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestNormalizeMAC(t *testing.T) {
	for in, want := range map[string]string{
		"aa:bb:cc:dd:ee:ff":  "AA-BB-CC-DD-EE-FF",
		"AA-BB-CC-DD-EE-FF":  "AA-BB-CC-DD-EE-FF",
		" aa-bb-cc-dd-ee-0f": "AA-BB-CC-DD-EE-0F",
	} {
		if got := client.NormalizeMAC(in); got != want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}
}
