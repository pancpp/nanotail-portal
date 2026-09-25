package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return data, err
}

type Client struct {
	binary    string
	socket    string
	timeout   time.Duration
	runner    Runner
	mutations chan struct{}
	// Protected by mutations; authentication URLs are never retained here.
	keyRenewal     *keyRenewalAttempt
	subnetDefaults SubnetDefaultsStore
	detectSubnets  func(context.Context) ([]string, error)
}

func NewClient(binary, socket string, timeout time.Duration, runner Runner) *Client {
	if runner == nil {
		runner = execRunner{}
	}
	return &Client{binary: binary, socket: socket, timeout: timeout, runner: runner, mutations: make(chan struct{}, 1)}
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.socket != "" {
		args = append([]string{"--socket=" + c.socket}, args...)
	}
	data, err := c.runner.Run(ctx, c.binary, args...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		// Do not return command output; it can contain daemon credentials.
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return data, nil
}

type Peer struct {
	ID                  string         `json:"id"`
	NodeID              int64          `json:"node_id"`
	PublicKey           string         `json:"public_key"`
	Hostname            string         `json:"hostname"`
	DNSName             string         `json:"dns_name"`
	OS                  string         `json:"os"`
	UserID              int64          `json:"user_id"`
	IPs                 []string       `json:"ips"`
	AllowedIPs          []string       `json:"allowed_ips"`
	Tags                []string       `json:"tags"`
	Addrs               []string       `json:"addrs"`
	CurAddr             string         `json:"cur_addr"`
	Relay               string         `json:"relay"`
	PeerRelay           string         `json:"peer_relay"`
	Online              bool           `json:"online"`
	Active              bool           `json:"active"`
	ExitNode            bool           `json:"exit_node"`
	ExitNodeOption      bool           `json:"exit_node_option"`
	RxBytes             int64          `json:"rx_bytes"`
	TxBytes             int64          `json:"tx_bytes"`
	Created             time.Time      `json:"created"`
	LastWrite           *time.Time     `json:"last_write,omitempty"`
	LastSeen            *time.Time     `json:"last_seen,omitempty"`
	LastHandshake       *time.Time     `json:"last_handshake,omitempty"`
	KeyExpiry           *time.Time     `json:"key_expiry,omitempty"`
	PeerAPIURL          []string       `json:"peer_api_url"`
	TaildropTarget      int32          `json:"taildrop_target"`
	NoFileSharingReason string         `json:"no_file_sharing_reason"`
	CapMap              map[string]any `json:"cap_map,omitempty"`
	InNetworkMap        bool           `json:"in_network_map"`
	InMagicSock         bool           `json:"in_magic_sock"`
	InEngine            bool           `json:"in_engine"`
}

type rawPeer struct {
	ID, PublicKey, HostName, DNSName, OS          string
	NodeID, UserID                                int64
	TailscaleIPs, AllowedIPs, Tags, Addrs         []string
	CurAddr, Relay, PeerRelay                     string
	Online, Active, ExitNode, ExitNodeOption      bool
	RxBytes, TxBytes                              int64
	Created                                       time.Time
	LastWrite, LastSeen, LastHandshake, KeyExpiry *time.Time
	PeerAPIURL                                    []string
	TaildropTarget                                int32
	NoFileSharingReason                           string
	CapMap                                        map[string]any
	InNetworkMap, InMagicSock, InEngine           bool
}

func (p rawPeer) peer() Peer {
	return Peer{
		ID: p.ID, Hostname: p.HostName, DNSName: p.DNSName, OS: p.OS, IPs: nonNil(p.TailscaleIPs),
		NodeID: p.NodeID, PublicKey: p.PublicKey, UserID: p.UserID,
		AllowedIPs: nonNil(p.AllowedIPs), Tags: nonNil(p.Tags), Addrs: p.Addrs,
		CurAddr: p.CurAddr, Relay: p.Relay, PeerRelay: p.PeerRelay,
		Online: p.Online, Active: p.Active, ExitNode: p.ExitNode, ExitNodeOption: p.ExitNodeOption,
		RxBytes: p.RxBytes, TxBytes: p.TxBytes, Created: p.Created,
		LastWrite: knownTime(p.LastWrite), LastSeen: knownTime(p.LastSeen),
		LastHandshake: knownTime(p.LastHandshake), KeyExpiry: knownTime(p.KeyExpiry),
		PeerAPIURL: nonNil(p.PeerAPIURL), TaildropTarget: p.TaildropTarget,
		NoFileSharingReason: p.NoFileSharingReason, CapMap: p.CapMap,
		InNetworkMap: p.InNetworkMap, InMagicSock: p.InMagicSock, InEngine: p.InEngine,
	}
}

// Tailscale uses zero timestamps for unknown times.
func knownTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	return value
}

type Tailnet struct {
	Name            string `json:"name"`
	MagicDNSSuffix  string `json:"magicDNSSuffix"`
	MagicDNSEnabled bool   `json:"magicDNSEnabled"`
}

type DNSRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type ClientVersion struct {
	RunningLatest        bool   `json:"runningLatest"`
	LatestVersion        string `json:"latestVersion"`
	UrgentSecurityUpdate bool   `json:"urgentSecurityUpdate"`
	Notify               bool   `json:"notify"`
	NotifyURL            string `json:"notifyURL"`
	NotifyText           string `json:"notifyText"`
}

type Status struct {
	Version        string         `json:"version"`
	TUN            bool           `json:"tun"`
	BackendState   string         `json:"backend_state"`
	HaveNodeKey    bool           `json:"have_node_key"`
	AuthURL        string         `json:"auth_url,omitempty"`
	IPs            []string       `json:"ips"`
	Health         []string       `json:"health"`
	Tailnet        string         `json:"tailnet"`
	MagicDNSSuffix string         `json:"magic_dns_suffix"`
	CurrentTailnet *Tailnet       `json:"current_tailnet"`
	CertDomains    []string       `json:"cert_domains"`
	ExtraRecords   []DNSRecord    `json:"extra_records"`
	ClientVersion  *ClientVersion `json:"client_version"`
	Self           *Peer          `json:"self"`
	Peers          []Peer         `json:"peers"`
	RxBytes        int64          `json:"rx_bytes"`
	TxBytes        int64          `json:"tx_bytes"`
	ObservedAt     time.Time      `json:"observed_at"`
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	data, err := c.run(ctx, "status", "--json")
	if err != nil {
		return Status{}, err
	}
	var raw struct {
		Version, BackendState, AuthURL string
		TUN, HaveNodeKey               bool
		TailscaleIPs, Health           []string
		MagicDNSSuffix                 string
		CurrentTailnet                 *Tailnet
		CertDomains                    []string
		ExtraRecords                   []DNSRecord
		ClientVersion                  *ClientVersion
		Self                           *rawPeer
		Peer                           map[string]*rawPeer
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.BackendState == "" {
		return Status{}, ErrInvalidOutput
	}
	status := Status{
		Version: raw.Version, BackendState: raw.BackendState, AuthURL: raw.AuthURL,
		TUN: raw.TUN, HaveNodeKey: raw.HaveNodeKey, MagicDNSSuffix: raw.MagicDNSSuffix,
		CurrentTailnet: raw.CurrentTailnet, CertDomains: raw.CertDomains,
		ExtraRecords: raw.ExtraRecords, ClientVersion: raw.ClientVersion,
		IPs: nonNil(raw.TailscaleIPs), Health: nonNil(raw.Health), Peers: []Peer{}, ObservedAt: time.Now().UTC(),
	}
	if raw.CurrentTailnet != nil {
		status.Tailnet = raw.CurrentTailnet.Name
	}
	if raw.Self != nil {
		self := raw.Self.peer()
		status.Self = &self
	}
	for _, peer := range raw.Peer {
		if peer != nil {
			status.Peers = append(status.Peers, peer.peer())
			status.RxBytes += peer.RxBytes
			status.TxBytes += peer.TxBytes
		}
	}
	sort.Slice(status.Peers, func(i, j int) bool {
		if status.Peers[i].Hostname == status.Peers[j].Hostname {
			return status.Peers[i].ID < status.Peers[j].ID
		}
		return status.Peers[i].Hostname < status.Peers[j].Hostname
	})
	return status, nil
}

type Config struct {
	WantRunning            bool     `json:"want_running"`
	Hostname               string   `json:"hostname"`
	AcceptDNS              bool     `json:"accept_dns"`
	AcceptRoutes           bool     `json:"accept_routes"`
	ShieldsUp              bool     `json:"shields_up"`
	ExitNode               string   `json:"exit_node"`
	ExitNodeID             string   `json:"exit_node_id"`
	ExitNodeAllowLANAccess bool     `json:"exit_node_allow_lan_access"`
	AdvertiseRoutes        []string `json:"advertise_routes"`
	AdvertiseExitNode      bool     `json:"advertise_exit_node"`
	SNATEnabled            bool     `json:"snat_enabled"`
	exitRoutes             []string
}

func (c *Client) Config(ctx context.Context) (Config, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	data, err := c.run(ctx, "debug", "prefs")
	if err != nil {
		return Config{}, err
	}
	// Read only allowlisted preferences; Persist contains private keys and must
	// never be forwarded to the browser. debug prefs supports older clients
	// that predate tailscale get.
	var raw struct {
		WantRunning                                          *bool
		Hostname                                             string
		CorpDNS, RouteAll, ShieldsUp, ExitNodeAllowLANAccess bool
		ExitNodeIP, ExitNodeID                               string
		AdvertiseRoutes                                      []string
		NoSNAT                                               bool
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.WantRunning == nil {
		return Config{}, ErrInvalidOutput
	}
	result := Config{
		WantRunning: *raw.WantRunning, Hostname: raw.Hostname, AcceptDNS: raw.CorpDNS,
		AcceptRoutes: raw.RouteAll, ShieldsUp: raw.ShieldsUp, ExitNode: raw.ExitNodeIP,
		ExitNodeID:             raw.ExitNodeID,
		ExitNodeAllowLANAccess: raw.ExitNodeAllowLANAccess, AdvertiseRoutes: []string{},
		SNATEnabled: !raw.NoSNAT,
	}
	for _, route := range raw.AdvertiseRoutes {
		if route == "0.0.0.0/0" || route == "::/0" {
			result.AdvertiseExitNode = true
			result.exitRoutes = append(result.exitRoutes, route)
		} else {
			result.AdvertiseRoutes = append(result.AdvertiseRoutes, route)
		}
	}
	return result, nil
}

// Pointer fields distinguish an omitted setting from false or an empty value.
type ConfigUpdate struct {
	Hostname               *string   `json:"hostname"`
	AcceptDNS              *bool     `json:"accept_dns"`
	AcceptRoutes           *bool     `json:"accept_routes"`
	ShieldsUp              *bool     `json:"shields_up"`
	ExitNode               *string   `json:"exit_node"`
	ExitNodeAllowLANAccess *bool     `json:"exit_node_allow_lan_access"`
	AdvertiseRoutes        *[]string `json:"advertise_routes"`
	AdvertiseExitNode      *bool     `json:"advertise_exit_node"`
}

func (u ConfigUpdate) args() ([]string, error) {
	args := []string{"set"}
	for _, option := range []struct {
		name  string
		value *bool
	}{
		{"accept-dns", u.AcceptDNS}, {"accept-routes", u.AcceptRoutes}, {"shields-up", u.ShieldsUp},
		{"exit-node-allow-lan-access", u.ExitNodeAllowLANAccess}, {"advertise-exit-node", u.AdvertiseExitNode},
	} {
		if option.value != nil {
			args = append(args, "--"+option.name+"="+strconv.FormatBool(*option.value))
		}
	}
	if u.Hostname != nil {
		if !hostnamePattern.MatchString(*u.Hostname) {
			return nil, fmt.Errorf("%w: hostname must be a DNS label of 1–63 characters", ErrInvalidConfig)
		}
		args = append(args, "--hostname="+*u.Hostname)
	}
	if u.ExitNode != nil {
		// Only addresses and DNS names are accepted, never arbitrary CLI flags.
		value := *u.ExitNode
		_, ipErr := netip.ParseAddr(value)
		validName := len(value) <= 253 && value != ""
		for _, label := range strings.Split(value, ".") {
			validName = validName && hostnamePattern.MatchString(label)
		}
		if value != "" && ipErr != nil && !validName {
			return nil, fmt.Errorf("%w: exit_node must be an IP address, DNS name or empty string", ErrInvalidConfig)
		}
		args = append(args, "--exit-node="+value)
	}
	if u.AdvertiseRoutes != nil {
		if len(*u.AdvertiseRoutes) > 64 {
			return nil, fmt.Errorf("%w: at most 64 subnet routes may be advertised", ErrInvalidConfig)
		}
		for _, value := range *u.AdvertiseRoutes {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix != prefix.Masked() || prefix.Bits() == 0 {
				return nil, fmt.Errorf("%w: routes must be canonical subnet CIDRs; use advertise_exit_node for default routes", ErrInvalidConfig)
			}
		}
		args = append(args, "--advertise-routes="+strings.Join(*u.AdvertiseRoutes, ","))
	}
	if len(args) == 1 {
		return nil, fmt.Errorf("%w: provide at least one setting", ErrInvalidConfig)
	}
	return args, nil
}

func (c *Client) UpdateConfig(ctx context.Context, update ConfigUpdate) error {
	// Even legacy configuration endpoints cannot change the appliance's role.
	if (update.AdvertiseExitNode != nil && !*update.AdvertiseExitNode) ||
		(update.ExitNode != nil && *update.ExitNode != "") ||
		(update.ExitNodeAllowLANAccess != nil && *update.ExitNodeAllowLANAccess) {
		return fmt.Errorf("%w: this device must advertise itself as an exit node and cannot use another exit node", ErrInvalidConfig)
	}
	args, err := update.args()
	if err != nil {
		return err
	}
	return c.mutateWithSubnetChoice(ctx, update.AdvertiseRoutes != nil, args...)
}

func (c *Client) Up(ctx context.Context) error {
	// A bare up preserves an authenticated node's existing preferences. Even
	// --timeout opts into the CLI's flag reconciliation, so use our context
	// deadline instead of passing any flags.
	return c.mutate(ctx, "up")
}

func (c *Client) Down(ctx context.Context) error {
	return c.mutate(ctx, "down")
}

func (c *Client) mutate(ctx context.Context, args ...string) error {
	return c.mutateWithSubnetChoice(ctx, false, args...)
}

func (c *Client) mutateWithSubnetChoice(ctx context.Context, subnetChoice bool, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case c.mutations <- struct{}{}:
		defer func() { <-c.mutations }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if subnetChoice {
		if err := c.claimSubnetChoice(ctx); err != nil {
			return err
		}
	}
	_, err := c.run(ctx, args...)
	return err
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
