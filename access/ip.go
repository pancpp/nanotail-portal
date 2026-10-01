package access

import (
	"bytes"
	"encoding/binary"
	"log"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

type ipv6Address struct {
	ip                net.IP
	temporary         bool
	unusable          bool
	preferredLifetime uint32
	validLifetime     uint32
}

// GetIPv4 returns the first usable IPv4 address on the named interface. An
// interface that is down or has no usable IPv4 address returns an empty string.
func GetIPv4(name string) (string, error) {
	iface, err := lanInterface(name)
	if err != nil {
		return "", err
	}
	if iface.Flags&net.FlagUp == 0 {
		return "", nil
	}

	addrs, err := iface.Addrs()
	if err != nil {
		log.Println("(ip) read interface addresses err:", name, err)
		return "", err
	}
	return findIPv4(addrs), nil
}

// GetIPv6 returns a preferred IPv6 service address on the named interface. An
// interface that is down or has no usable IPv6 address returns an empty string.
// Discovery errors, including missing lifetime metadata, remain errors.
func GetIPv6(name string) (string, error) {
	iface, err := lanInterface(name)
	if err != nil {
		return "", err
	}
	if iface.Flags&net.FlagUp == 0 {
		return "", nil
	}

	ipv6Addrs, err := readIPv6Addresses(iface.Index)
	if err != nil {
		log.Println("(ip) read IPv6 address metadata err:", name, err)
		return "", err
	}
	return findIPv6(ipv6Addrs), nil
}

func lanInterface(name string) (*net.Interface, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		log.Println("(ip) find network interface err:", name, err)
		return nil, err
	}
	if iface.Flags&net.FlagLoopback != 0 {
		log.Println("(ip) loopback interface err:", name, ErrFindLanIP)
		return nil, ErrFindLanIP
	}
	return iface, nil
}

func findIPv4(addrs []net.Addr) string {
	for _, addr := range addrs {
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
		default:
			continue
		}

		if ip4 := ip.To4(); ip4 != nil && ip4.IsGlobalUnicast() {
			return ip4.String()
		}
	}
	return ""
}

func findIPv6(ipv6Addrs []ipv6Address) string {
	var best ipv6Address
	for _, addr := range ipv6Addrs {
		if !addr.ip.IsGlobalUnicast() || addr.ip.To4() != nil || addr.unusable ||
			addr.preferredLifetime == 0 || addr.validLifetime == 0 || addr.preferredLifetime > addr.validLifetime {
			continue
		}
		// Non-temporary addresses always win. Equal candidates use the lowest
		// numeric address, without churning as remaining lifetimes count down.
		if best.ip == nil || (best.temporary && !addr.temporary) ||
			(best.temporary == addr.temporary && bytes.Compare(addr.ip.To16(), best.ip.To16()) < 0) {
			best = addr
		}
	}
	if best.ip != nil {
		return best.ip.String()
	}
	return ""
}

func readIPv6Addresses(interfaceIndex int) ([]ipv6Address, error) {
	dump, err := syscall.NetlinkRIB(unix.RTM_GETADDR, unix.AF_INET6)
	if err != nil {
		log.Println("(ip) read IPv6 netlink dump err:", err)
		return nil, err
	}
	return parseIPv6Addresses(dump, interfaceIndex)
}

func parseIPv6Addresses(dump []byte, interfaceIndex int) ([]ipv6Address, error) {
	messages, err := syscall.ParseNetlinkMessage(dump)
	if err != nil {
		log.Println("(ip) parse IPv6 netlink messages err:", err)
		return nil, err
	}
	var addresses []ipv6Address
	complete := false
	for _, message := range messages {
		if message.Header.Flags&unix.NLM_F_DUMP_INTR != 0 || message.Header.Type == unix.NLMSG_OVERRUN {
			log.Println("(ip) interrupted IPv6 netlink dump err:", ErrIncompleteAddressDump)
			return nil, ErrIncompleteAddressDump
		}
		switch message.Header.Type {
		case unix.NLMSG_DONE:
			if len(message.Data) >= 4 && binary.NativeEndian.Uint32(message.Data[:4]) != 0 {
				log.Println("(ip) IPv6 netlink dump status err:", int32(binary.NativeEndian.Uint32(message.Data[:4])))
				return nil, ErrIncompleteAddressDump
			}
			complete = true
		case unix.NLMSG_ERROR:
			log.Println("(ip) IPv6 netlink error response err:", ErrIncompleteAddressDump)
			return nil, ErrIncompleteAddressDump
		case unix.RTM_NEWADDR:
			if len(message.Data) < unix.SizeofIfAddrmsg {
				log.Println("(ip) short IPv6 address header err:", ErrInvalidAddressDump)
				return nil, ErrInvalidAddressDump
			}
			if message.Data[0] != unix.AF_INET6 || binary.NativeEndian.Uint32(message.Data[4:8]) != uint32(interfaceIndex) {
				continue
			}
			addr, err := parseIPv6Address(message.Data)
			if err != nil {
				log.Println("(ip) parse IPv6 address err:", err)
				return nil, err
			}
			addresses = append(addresses, addr)
		}
	}
	if !complete {
		log.Println("(ip) missing IPv6 dump completion err:", ErrIncompleteAddressDump)
		return nil, ErrIncompleteAddressDump
	}
	return addresses, nil
}

// Linux IFA_FLAGS overrides the byte-sized header flags. IFA_LOCAL is the local
// address when IFA_ADDRESS describes a point-to-point peer. All integer fields
// are native-endian; IFA_CACHEINFO stores remaining lifetimes in seconds.
func parseIPv6Address(data []byte) (ipv6Address, error) {
	var addr ipv6Address
	var localIP net.IP
	haveLifetimes := false
	flags := uint32(data[2])
	scope := data[3]
	attributes := data[unix.SizeofIfAddrmsg:]
	for len(attributes) > 0 {
		if len(attributes) < unix.SizeofRtAttr {
			log.Println("(ip) short IPv6 attribute header err:", ErrInvalidAddressDump)
			return ipv6Address{}, ErrInvalidAddressDump
		}
		length := int(binary.NativeEndian.Uint16(attributes[:2]))
		paddedLength := (length + 3) &^ 3
		if length < unix.SizeofRtAttr || paddedLength > len(attributes) {
			log.Println("(ip) invalid IPv6 attribute length err:", ErrInvalidAddressDump)
			return ipv6Address{}, ErrInvalidAddressDump
		}
		kind := binary.NativeEndian.Uint16(attributes[2:4])
		value := attributes[unix.SizeofRtAttr:length]
		switch kind {
		case unix.IFA_ADDRESS, unix.IFA_LOCAL:
			if len(value) != net.IPv6len {
				log.Println("(ip) invalid IPv6 address length err:", ErrInvalidAddressDump)
				return ipv6Address{}, ErrInvalidAddressDump
			}
			if kind == unix.IFA_LOCAL {
				localIP = net.IP(value)
			} else {
				addr.ip = net.IP(value)
			}
		case unix.IFA_FLAGS:
			if len(value) != 4 {
				log.Println("(ip) invalid IPv6 flags err:", ErrInvalidAddressDump)
				return ipv6Address{}, ErrInvalidAddressDump
			}
			flags = binary.NativeEndian.Uint32(value)
		case unix.IFA_CACHEINFO:
			if len(value) < unix.SizeofIfaCacheinfo {
				log.Println("(ip) invalid IPv6 lifetimes err:", ErrInvalidAddressDump)
				return ipv6Address{}, ErrInvalidAddressDump
			}
			addr.preferredLifetime = binary.NativeEndian.Uint32(value[:4])
			addr.validLifetime = binary.NativeEndian.Uint32(value[4:8])
			haveLifetimes = true
		}
		attributes = attributes[paddedLength:]
	}
	if localIP != nil {
		addr.ip = localIP
	}
	if addr.ip == nil || !haveLifetimes {
		log.Println("(ip) missing IPv6 address metadata err:", ErrInvalidAddressDump)
		return ipv6Address{}, ErrInvalidAddressDump
	}
	addr.temporary = flags&unix.IFA_F_TEMPORARY != 0
	addr.unusable = flags&(unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED|unix.IFA_F_DEPRECATED|unix.IFA_F_OPTIMISTIC) != 0 || scope >= unix.RT_SCOPE_LINK
	return addr, nil
}
