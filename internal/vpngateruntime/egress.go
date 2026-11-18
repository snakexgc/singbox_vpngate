package vpngateruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"

	"guiforcores/internal/vpngate"
)

// ip-api's free API provides HTTP only. The caller must supply the temporary
// candidate core's proxy client so the request travels inside that VPN tunnel.
const egressLookupURL = "http://ip-api.com/json/?fields=status,message,query,country,countryCode,regionName,city&lang=zh-CN"
const egressCacheTTL = 7 * 24 * time.Hour
const egressLanguage = "zh-CN"

type egressLocation struct {
	IP, Country, CountryCode, Region, City string
}

func resultForEgressPath(s vpngate.Settings, n vpngate.Node) vpngate.ProbeResult {
	result := resultFromNode(n)
	if n.EgressCacheKey != "" && n.EgressCacheKey != egressCacheKey(s, n) {
		result.EgressIP, result.EgressCountry, result.EgressCountryCode = "", "", ""
		result.EgressRegion, result.EgressCity, result.EgressError = "", "", ""
		result.EgressCheckedAt, result.EgressLanguage, result.EgressCacheKey = "", "", ""
	}
	return result
}

func egressCacheKey(s vpngate.Settings, n vpngate.Node) string {
	// A probe uses the candidate profile and upstream server/authentication.
	// Neither the executable's location nor TUN process rules change that path.
	upstream := s.Upstream
	upstream.ProcessPaths = nil
	return hashEgressKey(struct {
		Config   string
		Upstream vpngate.Upstream
	}{n.Config, upstream})
}

func migrateEgressCacheKey(s vpngate.Settings, n *vpngate.Node) {
	if n.EgressCacheKey == "" {
		return
	}
	// Match against the old persisted settings before resolving the portable core
	// at its new location. Keep the original timestamp and remaining lifetime.
	previous := hashEgressKey(struct {
		Config   string
		Upstream vpngate.Upstream
		CorePath string
	}{n.Config, s.Upstream, s.CorePath})
	if n.EgressCacheKey == previous {
		n.EgressCacheKey = egressCacheKey(s, *n)
	}
}

func hashEgressKey(input any) string {
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validEgressCache(s vpngate.Settings, n vpngate.Node, at time.Time) bool {
	checked, err := time.Parse(time.RFC3339, n.EgressCheckedAt)
	if err != nil || at.Before(checked) || !at.Before(checked.Add(egressCacheTTL)) || n.EgressLanguage != egressLanguage || n.EgressCacheKey != egressCacheKey(s, n) {
		return false
	}
	ip, err := netip.ParseAddr(n.EgressIP)
	if err != nil || !publicEgressIP(ip) || n.EgressCountry == "" || n.EgressCountry != safeEgressText(n.EgressCountry, 128) || len(n.EgressCountryCode) != 2 || n.EgressCountryCode[0] < 'A' || n.EgressCountryCode[0] > 'Z' || n.EgressCountryCode[1] < 'A' || n.EgressCountryCode[1] > 'Z' {
		return false
	}
	return n.EgressRegion == safeEgressText(n.EgressRegion, 128) && n.EgressCity == safeEgressText(n.EgressCity, 128)
}

func enrichEgress(ctx context.Context, client *http.Client, s vpngate.Settings, n vpngate.Node, result *vpngate.ProbeResult) {
	if validEgressCache(s, n, time.Now()) {
		result.EgressError = ""
		return
	}
	location, err := lookupEgress(ctx, client)
	if err != nil {
		// Retain the last measured exit and its date. A failed refresh must not
		// turn old data into a new seven-day success cache.
		result.EgressError = fmt.Sprintf("出口信息查询失败：%v", err)
		return
	}
	result.EgressIP = location.IP
	result.EgressCountry = location.Country
	result.EgressCountryCode = location.CountryCode
	result.EgressRegion = location.Region
	result.EgressCity = location.City
	result.EgressCheckedAt = now()
	result.EgressLanguage = egressLanguage
	result.EgressCacheKey = egressCacheKey(s, n)
	result.EgressError = ""
}

func lookupEgress(parent context.Context, client *http.Client) (egressLocation, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, egressLookupURL, nil)
	if err != nil {
		return egressLocation{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	// There is exactly one provider request per candidate: do not follow a
	// redirect or retry a rate-limited exit. Other candidates have their own exits.
	requestClient := *client
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := requestClient.Do(req)
	if err != nil {
		return egressLocation{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 400 && res.Header.Get("X-Rl") == "0" {
		if seconds, parseErr := strconv.Atoi(res.Header.Get("X-Ttl")); parseErr == nil && seconds > 0 {
			return egressLocation{}, fmt.Errorf("ip-api 限流（HTTP %d，请在 %d 秒后重新测试）", res.StatusCode, seconds)
		}
		return egressLocation{}, fmt.Errorf("ip-api 限流（HTTP %d）", res.StatusCode)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return egressLocation{}, fmt.Errorf("ip-api 返回 HTTP %d", res.StatusCode)
	}
	const maxBody = 16 << 10
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return egressLocation{}, err
	}
	if len(raw) > maxBody {
		return egressLocation{}, fmt.Errorf("ip-api 响应超过大小限制")
	}
	var data struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Query       string `json:"query"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		RegionName  string `json:"regionName"`
		City        string `json:"city"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return egressLocation{}, fmt.Errorf("ip-api 响应不是有效 JSON")
	}
	if data.Status != "success" {
		return egressLocation{}, fmt.Errorf("ip-api 查询失败：%s", safeEgressText(data.Message, 160))
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(data.Query))
	if err != nil || !publicEgressIP(ip) {
		return egressLocation{}, fmt.Errorf("ip-api 未返回有效的公网出口 IP")
	}
	country := strings.TrimSpace(data.Country)
	code := strings.TrimSpace(data.CountryCode)
	if country == "" || country != safeEgressText(country, 128) || len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return egressLocation{}, fmt.Errorf("ip-api 未返回有效的国家信息")
	}
	region, city := strings.TrimSpace(data.RegionName), strings.TrimSpace(data.City)
	if region != safeEgressText(region, 128) || city != safeEgressText(city, 128) {
		return egressLocation{}, fmt.Errorf("ip-api 返回的地区信息无效")
	}
	return egressLocation{IP: ip.Unmap().String(), Country: country, CountryCode: code, Region: region, City: city}, nil
}

func safeEgressText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, string(runes))
}

func publicEgressIP(ip netip.Addr) bool {
	if ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// General public IPv6 egress uses the currently allocated global-unicast
	// block. Protocol-specific addresses and translation prefixes do not identify
	// an ordinary public exit, even where IsGlobalUnicast returns true.
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	// Global-unicast also includes documentation, shared, benchmark and reserved
	// ranges. These cannot establish a real public VPN egress measurement.
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "3fff::/20"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}
