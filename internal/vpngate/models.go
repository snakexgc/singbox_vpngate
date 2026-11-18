package vpngate

import "sync"

type Upstream struct {
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	Protocol     string   `json:"protocol"`
	Username     string   `json:"username"`
	Password     string   `json:"password"`
	ProcessPaths []string `json:"processPaths"`
}
type Settings struct {
	Upstream           Upstream `json:"upstream"`
	PoolCapacity       int      `json:"poolCapacity"`
	RefreshMinutes     int      `json:"refreshMinutes"`
	RoutingPreset      string   `json:"routingPreset"`
	MixedPort          int      `json:"mixedPort"`
	TUNEnabled         bool     `json:"tunEnabled"`
	SystemProxyEnabled bool     `json:"systemProxyEnabled"`
	TestURL            string   `json:"testURL"`
	TestConcurrency    int      `json:"testConcurrency"`
	TestTimeoutSeconds int      `json:"testTimeoutSeconds"`
	RulesAutoUpdate    bool     `json:"rulesAutoUpdate"`
	RulesUpdateHours   int      `json:"rulesUpdateHours"`
	CNRulesURL         string   `json:"cnRulesURL"`
	GFWRulesURL        string   `json:"gfwRulesURL"`
	CorePath           string   `json:"corePath"`
}
type Node struct {
	ID                string `json:"id"`
	Hostname          string `json:"hostname"`
	IP                string `json:"ip"`
	Country           string `json:"country"`
	CountryCode       string `json:"countryCode"`
	Port              int    `json:"port"`
	Protocol          string `json:"protocol"`
	Score             int64  `json:"score"`
	Speed             int64  `json:"speed"`
	Config            string `json:"config,omitempty"`
	ConfigHash        string `json:"configHash"`
	Status            string `json:"status"`
	LatencyMs         *int64 `json:"latencyMs"`
	HandshakeMs       *int64 `json:"handshakeMs"`
	LastTested        string `json:"lastTested"`
	LastSeen          string `json:"lastSeen"`
	Error             string `json:"error"`
	FailureKind       string `json:"failureKind"`
	PathFingerprint   string `json:"pathFingerprint"`
	EgressIP          string `json:"egressIP"`
	EgressCountry     string `json:"egressCountry"`
	EgressCountryCode string `json:"egressCountryCode"`
	EgressRegion      string `json:"egressRegion"`
	EgressCity        string `json:"egressCity"`
	EgressError       string `json:"egressError"`
	EgressCheckedAt   string `json:"egressCheckedAt"`
	EgressLanguage    string `json:"egressLanguage"`
	EgressCacheKey    string `json:"egressCacheKey"`
}
type RuntimeState struct {
	Status             string `json:"status"`
	LogLevel           string `json:"logLevel"`
	Message            string `json:"message"`
	Protected          bool   `json:"protected"`
	ConnectedNodeID    string `json:"connectedNodeId"`
	StartedAt          string `json:"startedAt"`
	CoreVersion        string `json:"coreVersion"`
	UpstreamOwner      string `json:"upstreamOwner"`
	RequiresAdmin      bool   `json:"requiresAdmin"`
	TUNEnabled         bool   `json:"tunEnabled"`
	SystemProxyEnabled bool   `json:"systemProxyEnabled"`
}
type RepairState struct {
	Needed        bool   `json:"needed"`
	RequiresAdmin bool   `json:"requiresAdmin"`
	Message       string `json:"message"`
}
type Job struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Available  int    `json:"available"`
	Failed     int    `json:"failed"`
	Message    string `json:"message"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}
type RuleStatus struct {
	Ready     bool   `json:"ready"`
	UpdatedAt string `json:"updatedAt"`
	Error     string `json:"error"`
}
type RulesState struct {
	CN  RuleStatus `json:"cn"`
	GFW RuleStatus `json:"gfw"`
}
type ServiceState struct {
	Installed     bool   `json:"installed"`
	Running       bool   `json:"running"`
	Protected     bool   `json:"protected"`
	RequiresAdmin bool   `json:"requiresAdmin"`
	Message       string `json:"message"`
}
type State struct {
	Settings       Settings     `json:"settings"`
	LogLevel       string       `json:"logLevel"`
	Nodes          []Node       `json:"nodes"`
	SelectedNodeID string       `json:"selectedNodeId"`
	Runtime        RuntimeState `json:"runtime"`
	Jobs           []Job        `json:"jobs"`
	Rules          RulesState   `json:"rules"`
	Service        ServiceState `json:"service"`
	Repair         RepairState  `json:"repair"`
}
type ProbeResult struct {
	NodeID            string `json:"nodeId"`
	Status            string `json:"status"`
	LatencyMs         *int64 `json:"latencyMs"`
	HandshakeMs       *int64 `json:"handshakeMs"`
	LastTested        string `json:"lastTested"`
	Error             string `json:"error"`
	FailureKind       string `json:"failureKind"`
	PathFingerprint   string `json:"pathFingerprint"`
	EgressIP          string `json:"egressIP"`
	EgressCountry     string `json:"egressCountry"`
	EgressCountryCode string `json:"egressCountryCode"`
	EgressRegion      string `json:"egressRegion"`
	EgressCity        string `json:"egressCity"`
	EgressError       string `json:"egressError"`
	EgressCheckedAt   string `json:"egressCheckedAt"`
	EgressLanguage    string `json:"egressLanguage"`
	EgressCacheKey    string `json:"egressCacheKey"`
}
type ConfigOptions struct {
	Production     bool
	MixedPort      int
	ControllerPort int
	CNRulesPath    string
	GFWRulesPath   string
	LogPath        string
	LogLevel       string
}
type Pool struct {
	mu    sync.RWMutex
	nodes []Node
}
