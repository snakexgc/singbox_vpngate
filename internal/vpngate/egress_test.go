package vpngate

import (
	"encoding/json"
	"strings"
	"testing"
)

const measuredEgressJSON = `{"egressIP":"8.8.4.4","egressCountry":"美国","egressCountryCode":"US","egressRegion":"加利福尼亚州","egressCity":"山景城","egressError":"","egressCheckedAt":"2026-11-18T00:00:00Z","egressLanguage":"zh-CN","egressCacheKey":"measured-path"}`

func measuredEgressNode(t *testing.T) Node {
	t.Helper()
	var n Node
	if err := json.Unmarshal([]byte(measuredEgressJSON), &n); err != nil {
		t.Fatal(err)
	}
	n.ID, n.IP, n.Port, n.ConfigHash, n.Status = "one", "203.0.113.7", 443, "profile-a", "available"
	n.Country, n.CountryCode = "Japan", "JP"
	return n
}

func egressFields(t *testing.T, value any) map[string]string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, key := range []string{"egressIP", "egressCountry", "egressCountryCode", "egressRegion", "egressCity", "egressError", "egressCheckedAt", "egressLanguage", "egressCacheKey"} {
		var resultValue string
		if err = json.Unmarshal(fields[key], &resultValue); err != nil {
			t.Fatalf("missing egress contract field %s: %v", key, err)
		}
		result[key] = resultValue
	}
	return result
}

func TestPoolRetainsMeasuredEgressAcrossFeedRefresh(t *testing.T) {
	n := measuredEgressNode(t)
	p := NewPool([]Node{n})
	p.Merge([]Node{{ID: "replacement-id", IP: n.IP, Port: n.Port, ConfigHash: n.ConfigHash, Country: "Korea", CountryCode: "KR"}}, 2, "")
	actual := p.Snapshot()[0]
	if actual.IP != "203.0.113.7" || actual.Country != "Korea" || egressFields(t, actual)["egressIP"] != "8.8.4.4" || egressFields(t, actual)["egressCountry"] != "美国" {
		t.Fatalf("feed refresh replaced measured exit or endpoint metadata: %+v", actual)
	}
}

func TestPoolRetainsMeasuredEgressWhenProfileCommentsChange(t *testing.T) {
	for _, action := range []string{"merge", "dedupe"} {
		t.Run(action, func(t *testing.T) {
			n := measuredEgressNode(t)
			n.Config = "# previous feed comment\n" + ovpnFixture
			updated := Node{ID: n.ID, IP: n.IP, Port: n.Port, Config: "# refreshed feed comment\n" + ovpnFixture, ConfigHash: "profile-b", Status: "untested"}
			pool := NewPool([]Node{n})
			if action == "merge" {
				pool.Merge([]Node{updated}, 2, "")
			} else {
				pool = NewPool([]Node{n, updated})
			}
			actual := pool.Snapshot()[0]
			if actual.Status != n.Status || actual.EgressIP != n.EgressIP || actual.EgressRegion != n.EgressRegion || actual.EgressCheckedAt != n.EgressCheckedAt || actual.EgressCacheKey != n.EgressCacheKey {
				t.Fatalf("ignored profile comment discarded the measured exit after %s: status=%s ip=%q region=%q checked=%q", action, actual.Status, actual.EgressIP, actual.EgressRegion, actual.EgressCheckedAt)
			}
		})
	}
}

func TestPoolPreservesMeasuredEgressIndependentlyOfTransientHealth(t *testing.T) {
	for _, action := range []string{"retest", "failed-retest", "canceled-retest", "path-reset"} {
		t.Run(action, func(t *testing.T) {
			n := measuredEgressNode(t)
			p := NewPool([]Node{n})
			switch action {
			case "retest":
				p.UpdateProbe(ProbeResult{NodeID: n.ID, Status: "testing"})
			case "failed-retest":
				p.UpdateProbe(ProbeResult{NodeID: n.ID, Status: "failed"})
			case "canceled-retest":
				p.UpdateProbe(ProbeResult{NodeID: n.ID, Status: "canceled"})
			case "path-reset":
				p.UpdateProbe(ProbeResult{NodeID: n.ID, Status: "untested"})
			}
			for field, value := range egressFields(t, p.Snapshot()[0]) {
				if value != egressFields(t, n)[field] {
					t.Fatalf("cached %s was discarded after %s: %q", field, action, value)
				}
			}
		})
	}
}

func TestChangedProfileInvalidatesDisplayedEgress(t *testing.T) {
	for _, action := range []string{"merge", "dedupe"} {
		t.Run(action, func(t *testing.T) {
			n := measuredEgressNode(t)
			n.Config = ovpnFixture
			updated := n
			updated.Config = strings.Replace(n.Config, "fixture-key", "different-key", 1)
			updated.ConfigHash, updated.Status = "new-profile", "untested"
			p := NewPool([]Node{n})
			if action == "merge" {
				p.Merge([]Node{updated}, 2, "")
			} else {
				p = NewPool([]Node{n, updated})
			}
			for field, value := range egressFields(t, p.Snapshot()[0]) {
				if value != "" {
					t.Fatalf("previous profile's %s still displayed after %s: %q", field, action, value)
				}
			}
		})
	}
}

func TestPoolAppliesMeasuredEgressWithoutOverwritingVPNGateMetadata(t *testing.T) {
	n := measuredEgressNode(t)
	var result ProbeResult
	if err := json.Unmarshal([]byte(measuredEgressJSON), &result); err != nil {
		t.Fatal(err)
	}
	result.NodeID, result.Status = n.ID, "available"
	p := NewPool([]Node{{ID: n.ID, IP: n.IP, Country: n.Country, CountryCode: n.CountryCode}})
	if !p.UpdateProbe(result) {
		t.Fatal("probe was not applied")
	}
	actual := p.Snapshot()[0]
	if actual.IP != "203.0.113.7" || actual.Country != "Japan" || actual.CountryCode != "JP" || egressFields(t, actual)["egressCity"] != "山景城" {
		t.Fatalf("exit result missing or source metadata changed: %+v", actual)
	}
}
