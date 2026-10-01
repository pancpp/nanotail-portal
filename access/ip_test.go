package access

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFindIPv4AndIPv6(t *testing.T) {
	t.Parallel()

	ipNet := func(ip string) net.Addr {
		return &net.IPNet{IP: net.ParseIP(ip)}
	}
	tests := []struct {
		name     string
		addrs    []net.Addr
		wantIPv4 string
		wantIPv6 string
	}{
		{name: "192.168 private range", addrs: []net.Addr{ipNet("192.168.1.10")}, wantIPv4: "192.168.1.10"},
		{name: "10 private range", addrs: []net.Addr{ipNet("10.0.0.10")}, wantIPv4: "10.0.0.10"},
		{name: "172.16 private range", addrs: []net.Addr{ipNet("172.16.0.10")}, wantIPv4: "172.16.0.10"},
		{name: "public IPv4", addrs: []net.Addr{ipNet("203.0.113.10")}, wantIPv4: "203.0.113.10"},
		{name: "global IPv6", addrs: []net.Addr{ipNet("2001:db8::10")}, wantIPv6: "2001:db8::10"},
		{name: "unique-local IPv6", addrs: []net.Addr{ipNet("fd00::10")}, wantIPv6: "fd00::10"},
		{
			name: "IPAddr values",
			addrs: []net.Addr{
				&net.IPAddr{IP: net.ParseIP("192.168.1.10")},
				&net.IPAddr{IP: net.ParseIP("2001:db8::10")},
			},
			wantIPv4: "192.168.1.10", wantIPv6: "2001:db8::10",
		},
		{
			name:     "IPv6 before IPv4",
			addrs:    []net.Addr{ipNet("2001:db8::10"), ipNet("192.168.1.10")},
			wantIPv4: "192.168.1.10", wantIPv6: "2001:db8::10",
		},
		{
			name: "first IPv4 and numeric IPv6 selection",
			addrs: []net.Addr{
				ipNet("192.168.1.10"), ipNet("10.0.0.10"),
				ipNet("2001:db8::10"), ipNet("fd00::10"),
			},
			wantIPv4: "192.168.1.10", wantIPv6: "2001:db8::10",
		},
		{
			name: "numeric IPv6 selection while finding IPv4",
			addrs: []net.Addr{
				ipNet("fd00::10"), ipNet("2001:db8::10"), ipNet("192.168.1.10"),
			},
			wantIPv4: "192.168.1.10", wantIPv6: "2001:db8::10",
		},
		{
			name:     "IPv4-mapped IPv6 belongs to IPv4",
			addrs:    []net.Addr{ipNet("::ffff:192.168.1.10")},
			wantIPv4: "192.168.1.10",
		},
		{
			name: "skip unsuitable addresses",
			addrs: []net.Addr{
				ipNet("127.0.0.1"), ipNet("169.254.1.1"), ipNet("0.0.0.0"),
				ipNet("224.0.0.1"), ipNet("255.255.255.255"),
				ipNet("::1"), ipNet("fe80::1"), ipNet("::"), ipNet("ff02::1"),
				nil, (*net.IPNet)(nil), (*net.IPAddr)(nil), &net.IPNet{},
				&net.UnixAddr{Name: "socket", Net: "unix"},
				ipNet("192.168.1.10"), ipNet("fd00::10"),
			},
			wantIPv4: "192.168.1.10", wantIPv6: "fd00::10",
		},
		{name: "no addresses"},
		{name: "unrecognized address type", addrs: []net.Addr{&net.UnixAddr{Name: "socket", Net: "unix"}}},
		{name: "missing IP", addrs: []net.Addr{&net.IPNet{}, &net.IPAddr{}}},
		{name: "nil addresses", addrs: []net.Addr{nil, (*net.IPNet)(nil), (*net.IPAddr)(nil)}},
		{name: "malformed IP", addrs: []net.Addr{&net.IPNet{IP: net.IP{1, 2, 3}}}},
		{name: "IPv4 loopback", addrs: []net.Addr{ipNet("127.0.0.1")}},
		{name: "IPv6 loopback", addrs: []net.Addr{ipNet("::1")}},
		{name: "IPv4 link-local", addrs: []net.Addr{ipNet("169.254.1.1")}},
		{name: "IPv6 link-local", addrs: []net.Addr{ipNet("fe80::1")}},
		{name: "IPv4 unspecified", addrs: []net.Addr{ipNet("0.0.0.0")}},
		{name: "IPv6 unspecified", addrs: []net.Addr{ipNet("::")}},
		{name: "IPv4 multicast", addrs: []net.Addr{ipNet("224.0.0.1")}},
		{name: "IPv6 multicast", addrs: []net.Addr{ipNet("ff02::1")}},
		{name: "IPv4 broadcast", addrs: []net.Addr{ipNet("255.255.255.255")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Supply known-good lifetime metadata for these address-class fixtures.
			var ipv6Addrs []ipv6Address
			for _, addr := range tt.addrs {
				var ip net.IP
				switch addr := addr.(type) {
				case *net.IPNet:
					if addr != nil {
						ip = addr.IP
					}
				case *net.IPAddr:
					if addr != nil {
						ip = addr.IP
					}
				}
				ipv6Addrs = append(ipv6Addrs, ipv6Address{ip: ip, preferredLifetime: 300, validLifetime: 600})
			}
			if ipv4 := findIPv4(tt.addrs); ipv4 != tt.wantIPv4 {
				t.Errorf("findIPv4() = %q, want %q", ipv4, tt.wantIPv4)
			}
			if ipv6 := findIPv6(ipv6Addrs); ipv6 != tt.wantIPv6 {
				t.Errorf("findIPv6() = %q, want %q", ipv6, tt.wantIPv6)
			}
		})
	}
}

func TestFindIPv6Preference(t *testing.T) {
	t.Parallel()
	stable := ipv6Address{ip: net.ParseIP("2001:db8::10"), preferredLifetime: 300, validLifetime: 600}
	temporary := ipv6Address{ip: net.ParseIP("2001:db8::1"), temporary: true, preferredLifetime: 400, validLifetime: 700}
	ula := ipv6Address{ip: net.ParseIP("fd00::1"), preferredLifetime: 300, validLifetime: 600}
	for _, tt := range []struct {
		name  string
		addrs []ipv6Address
		want  string
	}{
		{name: "stable beats temporary with longer lifetime", addrs: []ipv6Address{temporary, stable}, want: "2001:db8::10"},
		{name: "temporary fallback", addrs: []ipv6Address{temporary}, want: "2001:db8::1"},
		{name: "stable ULA beats temporary global", addrs: []ipv6Address{temporary, ula}, want: "fd00::1"},
		{name: "ULA remains eligible", addrs: []ipv6Address{ula}, want: "fd00::1"},
		{name: "stable tie uses numeric address", addrs: []ipv6Address{stable, {ip: net.ParseIP("2001:db8::2"), preferredLifetime: 10, validLifetime: 20}}, want: "2001:db8::2"},
		{name: "temporary tie uses numeric address", addrs: []ipv6Address{temporary, {ip: net.ParseIP("2001:db8::20"), temporary: true, preferredLifetime: 800, validLifetime: 900}}, want: "2001:db8::1"},
		{name: "both stable scopes use numeric address", addrs: []ipv6Address{ula, stable}, want: "2001:db8::10"},
		{name: "no IPv6 metadata"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addrs := append([]ipv6Address(nil), tt.addrs...)
			for range 2 {
				if ipv6 := findIPv6(addrs); ipv6 != tt.want {
					t.Fatalf("findIPv6() = %q, want %q", ipv6, tt.want)
				}
				for i, j := 0, len(addrs)-1; i < j; i, j = i+1, j-1 {
					addrs[i], addrs[j] = addrs[j], addrs[i]
				}
			}
		})
	}
}

func TestFindIPv6RejectsUnusableAddresses(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		addr ipv6Address
	}{
		{name: "bad address state", addr: ipv6Address{unusable: true, preferredLifetime: 300, validLifetime: 600}},
		{name: "preferred lifetime expired", addr: ipv6Address{validLifetime: 600}},
		{name: "valid lifetime expired", addr: ipv6Address{preferredLifetime: 300}},
		{name: "both lifetimes expired"},
		{name: "invalid lifetime ordering", addr: ipv6Address{preferredLifetime: 600, validLifetime: 300}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.addr.ip = net.ParseIP("2001:db8::1")
			if ipv6 := findIPv6([]ipv6Address{tt.addr}); ipv6 != "" {
				t.Fatalf("unusable address selected: %q", ipv6)
			}
			fallback := ipv6Address{ip: net.ParseIP("2001:db8::2"), temporary: true, preferredLifetime: 100, validLifetime: 200}
			if ipv6 := findIPv6([]ipv6Address{tt.addr, fallback}); ipv6 != "2001:db8::2" {
				t.Fatalf("unusable stable address blocks temporary fallback: %q", ipv6)
			}
		})
	}
}

func TestGetIPInvalidInterface(t *testing.T) {
	t.Parallel()

	for _, getter := range []struct {
		name string
		get  func(string) (string, error)
	}{
		{name: "GetIPv4", get: GetIPv4},
		{name: "GetIPv6", get: GetIPv6},
	} {
		t.Run(getter.name, func(t *testing.T) {
			t.Parallel()
			for _, name := range []string{"", "nonexistent-nanotail-test-interface"} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					ip, err := getter.get(name)
					if ip != "" {
						t.Errorf("%s(%q) = %q, want empty address on error", getter.name, name, ip)
					}
					if err == nil {
						t.Fatalf("%s(%q) succeeded, want interface lookup error", getter.name, name)
					}
					var netErr *net.OpError
					if !errors.As(err, &netErr) {
						t.Errorf("error = %v, want a network operation error", err)
					}
				})
			}
		})
	}
}

func TestGetIPRejectsLoopback(t *testing.T) {
	t.Parallel()

	interfaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot inspect local interfaces: %v", err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback == 0 {
			continue
		}
		for _, getter := range []struct {
			name string
			get  func(string) (string, error)
		}{
			{name: "GetIPv4", get: GetIPv4},
			{name: "GetIPv6", get: GetIPv6},
		} {
			t.Run(getter.name, func(t *testing.T) {
				t.Parallel()
				ip, err := getter.get(iface.Name)
				if ip != "" || !errors.Is(err, ErrFindLanIP) {
					t.Errorf("%s(%q) = (%q, %v), want empty address and ErrFindLanIP", getter.name, iface.Name, ip, err)
				}
			})
		}
		return
	}
	t.Skip("no loopback interface available")
}

const (
	TEST_INTERFACE_INDEX = 7
	INFINITE_LIFETIME    = ^uint32(0)
)

func addressAttribute(kind uint16, value []byte) []byte {
	length := unix.SizeofRtAttr + len(value)
	data := make([]byte, (length+3)&^3)
	binary.NativeEndian.PutUint16(data[:2], uint16(length))
	binary.NativeEndian.PutUint16(data[2:4], kind)
	copy(data[4:], value)
	return data
}

func addressMessage(kind, flags uint16, value []byte) []byte {
	length := unix.NLMSG_HDRLEN + len(value)
	data := make([]byte, (length+3)&^3)
	binary.NativeEndian.PutUint32(data[:4], uint32(length))
	binary.NativeEndian.PutUint16(data[4:6], kind)
	binary.NativeEndian.PutUint16(data[6:8], flags)
	copy(data[unix.NLMSG_HDRLEN:], value)
	return data
}

func ipv6AddressData(ip string, flags, preferred, valid uint32) []byte {
	data := make([]byte, unix.SizeofIfAddrmsg)
	data[0], data[1], data[2] = unix.AF_INET6, 64, byte(flags)
	binary.NativeEndian.PutUint32(data[4:8], TEST_INTERFACE_INDEX)
	data = append(data, addressAttribute(unix.IFA_ADDRESS, net.ParseIP(ip).To16())...)
	data = append(data, addressAttribute(unix.IFA_FLAGS, binary.NativeEndian.AppendUint32(nil, flags))...)
	lifetimes := make([]byte, unix.SizeofIfaCacheinfo)
	binary.NativeEndian.PutUint32(lifetimes[:4], preferred)
	binary.NativeEndian.PutUint32(lifetimes[4:8], valid)
	return append(data, addressAttribute(unix.IFA_CACHEINFO, lifetimes)...)
}

func completeAddressDump(data ...[]byte) []byte {
	var dump []byte
	for _, addr := range data {
		dump = append(dump, addressMessage(unix.RTM_NEWADDR, 0, addr)...)
	}
	return append(dump, addressMessage(unix.NLMSG_DONE, 0, make([]byte, 4))...)
}

func TestIPv6NetlinkFlagsAndLifetimes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		flags     uint32
		preferred uint32
		valid     uint32
		usable    bool
		temporary bool
	}{
		{name: "dynamic stable", preferred: 300, valid: 600, usable: true},
		{name: "permanent", flags: unix.IFA_F_PERMANENT, preferred: INFINITE_LIFETIME, valid: INFINITE_LIFETIME, usable: true},
		{name: "mngtmpaddr is stable", flags: unix.IFA_F_MANAGETEMPADDR, preferred: 300, valid: 600, usable: true},
		{name: "stable privacy is stable", flags: unix.IFA_F_STABLE_PRIVACY | unix.IFA_F_NOPREFIXROUTE, preferred: 300, valid: 600, usable: true},
		{name: "nodad", flags: unix.IFA_F_NODAD, preferred: 300, valid: 600, usable: true},
		{name: "temporary", flags: unix.IFA_F_TEMPORARY, preferred: 300, valid: 600, usable: true, temporary: true},
		{name: "tentative", flags: unix.IFA_F_TENTATIVE, preferred: 300, valid: 600},
		{name: "optimistic DAD", flags: unix.IFA_F_TENTATIVE | unix.IFA_F_OPTIMISTIC, preferred: 300, valid: 600},
		{name: "DAD failed", flags: unix.IFA_F_DADFAILED, preferred: 300, valid: 600},
		{name: "deprecated flag", flags: unix.IFA_F_DEPRECATED, preferred: 300, valid: 600},
		{name: "expired preferred lifetime", valid: 600},
		{name: "expired valid lifetime", preferred: 300},
		{name: "expired both lifetimes"},
		{name: "invalid lifetimes", preferred: 600, valid: 300},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := ipv6AddressData("2001:db8::1", tt.flags, tt.preferred, tt.valid)
			addrs, err := parseIPv6Addresses(completeAddressDump(data), TEST_INTERFACE_INDEX)
			if err != nil || len(addrs) != 1 {
				t.Fatalf("parseIPv6Addresses() = (%v, %v)", addrs, err)
			}
			if addrs[0].temporary != tt.temporary || addrs[0].preferredLifetime != tt.preferred || addrs[0].validLifetime != tt.valid {
				t.Fatalf("incorrect metadata: %+v", addrs[0])
			}
			ip := findIPv6(addrs)
			if (ip != "") != tt.usable {
				t.Fatalf("selected IPv6 = %q, usable = %v", ip, tt.usable)
			}
		})
	}
}

func TestIPv6NetlinkInterfaceAndAddressMetadata(t *testing.T) {
	t.Parallel()
	local := ipv6AddressData("2001:db8::1", unix.IFA_F_MANAGETEMPADDR, 300, 600)
	// Extended flags replace header flags, rather than being ORed together.
	local[2] = unix.IFA_F_TEMPORARY | unix.IFA_F_DEPRECATED
	local = append(local, addressAttribute(unix.IFA_LOCAL, net.ParseIP("fd00::2").To16())...)
	local = append(local, addressAttribute(unix.IFA_LABEL, []byte("eth0\x00"))...)
	other := ipv6AddressData("2001:db8::3", 0, 300, 600)
	binary.NativeEndian.PutUint32(other[4:8], TEST_INTERFACE_INDEX+1)
	ipv4 := ipv6AddressData("192.0.2.1", 0, 300, 600)
	ipv4[0] = unix.AF_INET
	addrs, err := parseIPv6Addresses(completeAddressDump(other, ipv4, local), TEST_INTERFACE_INDEX)
	if err != nil || len(addrs) != 1 {
		t.Fatalf("parseIPv6Addresses() = (%v, %v)", addrs, err)
	}
	ip := findIPv6(addrs)
	if ip != "fd00::2" || addrs[0].temporary {
		t.Fatalf("local address/extended flags not honored: %+v", addrs[0])
	}

	// Older kernels can omit IFA_FLAGS; the byte-sized header remains authoritative.
	headerFlags := ipv6AddressData("2001:db8::4", unix.IFA_F_TENTATIVE, 300, 600)
	const FLAGS_OFFSET = unix.SizeofIfAddrmsg + unix.SizeofRtAttr + net.IPv6len
	headerFlags = append(headerFlags[:FLAGS_OFFSET:FLAGS_OFFSET], headerFlags[FLAGS_OFFSET+8:]...)
	addrs, err = parseIPv6Addresses(completeAddressDump(headerFlags), TEST_INTERFACE_INDEX)
	if err != nil || len(addrs) != 1 || !addrs[0].unusable {
		t.Fatalf("header flags not honored: %v, %v", addrs, err)
	}
}

func TestIPv6NetlinkRejectsIncompleteSnapshots(t *testing.T) {
	t.Parallel()
	valid := ipv6AddressData("2001:db8::1", 0, 300, 600)
	shortFlags := append([]byte(nil), valid[:unix.SizeofIfAddrmsg]...)
	shortFlags = append(shortFlags, addressAttribute(unix.IFA_FLAGS, []byte{1})...)
	shortAddress := append([]byte(nil), valid[:unix.SizeofIfAddrmsg]...)
	shortAddress = append(shortAddress, addressAttribute(unix.IFA_ADDRESS, []byte{1, 2, 3})...)
	shortLifetimes := append([]byte(nil), valid[:unix.SizeofIfAddrmsg]...)
	shortLifetimes = append(shortLifetimes, addressAttribute(unix.IFA_CACHEINFO, []byte{1, 2, 3})...)
	for _, tt := range []struct {
		name string
		dump []byte
		want error
	}{
		{name: "missing completion", dump: addressMessage(unix.RTM_NEWADDR, 0, valid), want: ErrIncompleteAddressDump},
		{name: "interrupted address dump", dump: append(addressMessage(unix.RTM_NEWADDR, unix.NLM_F_DUMP_INTR, valid), addressMessage(unix.NLMSG_DONE, 0, nil)...), want: ErrIncompleteAddressDump},
		{name: "interrupted completion", dump: append(addressMessage(unix.RTM_NEWADDR, 0, valid), addressMessage(unix.NLMSG_DONE, unix.NLM_F_DUMP_INTR, nil)...), want: ErrIncompleteAddressDump},
		{name: "dump completion error", dump: addressMessage(unix.NLMSG_DONE, 0, binary.NativeEndian.AppendUint32(nil, 1)), want: ErrIncompleteAddressDump},
		{name: "netlink error", dump: addressMessage(unix.NLMSG_ERROR, 0, nil), want: ErrIncompleteAddressDump},
		{name: "overrun", dump: addressMessage(unix.NLMSG_OVERRUN, 0, nil), want: ErrIncompleteAddressDump},
		{name: "short address header", dump: completeAddressDump(valid[:3]), want: ErrInvalidAddressDump},
		{name: "short attribute header", dump: completeAddressDump(append(append([]byte(nil), valid...), 1)), want: ErrInvalidAddressDump},
		{name: "invalid attribute length", dump: completeAddressDump(append(append([]byte(nil), valid...), 0, 0, 0, 0)), want: ErrInvalidAddressDump},
		{name: "missing lifetimes", dump: completeAddressDump(valid[:len(valid)-20]), want: ErrInvalidAddressDump},
		{name: "missing address", dump: completeAddressDump(valid[:8]), want: ErrInvalidAddressDump},
		{name: "short flags", dump: completeAddressDump(shortFlags), want: ErrInvalidAddressDump},
		{name: "short address", dump: completeAddressDump(shortAddress), want: ErrInvalidAddressDump},
		{name: "short lifetimes", dump: completeAddressDump(shortLifetimes), want: ErrInvalidAddressDump},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addrs, err := parseIPv6Addresses(tt.dump, TEST_INTERFACE_INDEX)
			if !errors.Is(err, tt.want) || len(addrs) != 0 {
				t.Fatalf("parseIPv6Addresses() = (%v, %v), want no partial results and %v", addrs, err, tt.want)
			}
		})
	}
	addrs, err := parseIPv6Addresses(completeAddressDump(), TEST_INTERFACE_INDEX)
	if err != nil || len(addrs) != 0 {
		t.Fatalf("empty complete snapshot = (%v, %v), want success", addrs, err)
	}
}

func TestReadIPv6AddressesFromKernel(t *testing.T) {
	t.Parallel()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) == 0 {
		t.Skip("no local network interfaces")
	}
	for _, iface := range interfaces {
		if _, err := readIPv6Addresses(iface.Index); err != nil {
			t.Fatalf("readIPv6Addresses(%s): %v", iface.Name, err)
		}
	}
}

func TestGetIPFromKernel(t *testing.T) {
	t.Parallel()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, getter := range []struct {
		name string
		get  func(string) (string, error)
		ipv4 bool
	}{
		{name: "GetIPv4", get: GetIPv4, ipv4: true},
		{name: "GetIPv6", get: GetIPv6},
	} {
		t.Run(getter.name, func(t *testing.T) {
			t.Parallel()
			found := false
			for _, iface := range interfaces {
				if iface.Flags&net.FlagLoopback != 0 {
					continue
				}
				found = true
				ip, err := getter.get(iface.Name)
				if err != nil {
					t.Fatalf("%s(%s): %v", getter.name, iface.Name, err)
				}
				if iface.Flags&net.FlagUp == 0 && ip != "" {
					t.Fatalf("%s(%s) = %q, want empty address for down interface", getter.name, iface.Name, ip)
				}
				if ip != "" {
					parsed := net.ParseIP(ip)
					if parsed == nil || !parsed.IsGlobalUnicast() || (parsed.To4() != nil) != getter.ipv4 {
						t.Fatalf("%s(%s) returned unsuitable address %q", getter.name, iface.Name, ip)
					}
				}
				t.Logf("%s(%s) = %q", getter.name, iface.Name, ip)
			}
			if !found {
				t.Skip("no non-loopback network interfaces available")
			}
		})
	}
}
