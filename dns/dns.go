package dns

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// DNS Record Types
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypeMX    uint16 = 15
	TypeAAAA  uint16 = 28
	TypeTXT   uint16 = 16

	ClassIN uint16 = 1

	// Response codes
	RcodeNoError  = 0
	RcodeNXDomain = 3
	RcodeServFail = 2
	RcodeRefused  = 5
)

// Header represents a DNS message header (12 bytes).
type Header struct {
	ID      uint16
	Flags   uint16
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16
}

// Question represents a DNS question section entry.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// RR represents a DNS resource record.
type RR struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	RData string
}

// Message is a complete DNS message.
type Message struct {
	Header     Header
	Questions  []Question
	Answers    []RR
	Authority  []RR
	Additional []RR
}

// Rcode returns the response code from the header flags.
func (m *Message) Rcode() int {
	return int(m.Header.Flags & 0x000F)
}

// IsResponse returns true if QR bit is set.
func (m *Message) IsResponse() bool {
	return m.Header.Flags&0x8000 != 0
}

// IsAuthoritative returns true if AA bit is set.
func (m *Message) IsAuthoritative() bool {
	return m.Header.Flags&0x0400 != 0
}

// TypeName maps a DNS type code to a human-readable string.
func TypeName(t uint16) string {
	switch t {
	case TypeA:
		return "A"
	case TypeNS:
		return "NS"
	case TypeCNAME:
		return "CNAME"
	case TypeSOA:
		return "SOA"
	case TypeMX:
		return "MX"
	case TypeAAAA:
		return "AAAA"
	case TypeTXT:
		return "TXT"
	default:
		return fmt.Sprintf("TYPE%d", t)
	}
}

// TypeFromString converts a type name string to its uint16 code.
func TypeFromString(s string) uint16 {
	switch strings.ToUpper(s) {
	case "A":
		return TypeA
	case "NS":
		return TypeNS
	case "CNAME":
		return TypeCNAME
	case "MX":
		return TypeMX
	case "AAAA":
		return TypeAAAA
	case "TXT":
		return TypeTXT
	default:
		return TypeA
	}
}

// RcodeString maps an rcode to a human-readable name.
func RcodeString(r int) string {
	switch r {
	case RcodeNoError:
		return "NOERROR"
	case RcodeNXDomain:
		return "NXDOMAIN"
	case RcodeServFail:
		return "SERVFAIL"
	case RcodeRefused:
		return "REFUSED"
	default:
		return fmt.Sprintf("RCODE%d", r)
	}
}

// BuildQuery constructs a raw DNS query packet.
func BuildQuery(id uint16, domain string, qtype uint16) ([]byte, error) {
	buf := make([]byte, 0, 512)

	// Header: ID, Flags (RD=1), QDCOUNT=1, rest 0
	flags := uint16(0x0100) // recursion desired
	buf = appendUint16(buf, id)
	buf = appendUint16(buf, flags)
	buf = appendUint16(buf, 1) // QDCOUNT
	buf = appendUint16(buf, 0) // ANCOUNT
	buf = appendUint16(buf, 0) // NSCOUNT
	buf = appendUint16(buf, 0) // ARCOUNT

	// Question
	encoded, err := encodeName(domain)
	if err != nil {
		return nil, err
	}
	buf = append(buf, encoded...)
	buf = appendUint16(buf, qtype)
	buf = appendUint16(buf, ClassIN)

	return buf, nil
}

// Parse decodes a raw DNS message from wire format.
func Parse(data []byte) (*Message, error) {
	if len(data) < 12 {
		return nil, errors.New("dns: message too short")
	}

	msg := &Message{}
	msg.Header.ID = binary.BigEndian.Uint16(data[0:2])
	msg.Header.Flags = binary.BigEndian.Uint16(data[2:4])
	msg.Header.QDCount = binary.BigEndian.Uint16(data[4:6])
	msg.Header.ANCount = binary.BigEndian.Uint16(data[6:8])
	msg.Header.NSCount = binary.BigEndian.Uint16(data[8:10])
	msg.Header.ARCount = binary.BigEndian.Uint16(data[10:12])

	offset := 12

	// Parse questions
	for i := 0; i < int(msg.Header.QDCount); i++ {
		name, newOffset, err := decodeName(data, offset)
		if err != nil {
			return nil, fmt.Errorf("dns: question name: %w", err)
		}
		offset = newOffset
		if offset+4 > len(data) {
			return nil, errors.New("dns: truncated question")
		}
		q := Question{
			Name:  name,
			Type:  binary.BigEndian.Uint16(data[offset : offset+2]),
			Class: binary.BigEndian.Uint16(data[offset+2 : offset+4]),
		}
		offset += 4
		msg.Questions = append(msg.Questions, q)
	}

	// Parse answer, authority, additional sections
	sections := []struct {
		count uint16
		dest  *[]RR
	}{
		{msg.Header.ANCount, &msg.Answers},
		{msg.Header.NSCount, &msg.Authority},
		{msg.Header.ARCount, &msg.Additional},
	}
	for _, sec := range sections {
		for i := 0; i < int(sec.count); i++ {
			rr, newOffset, err := parseRR(data, offset)
			if err != nil {
				return nil, fmt.Errorf("dns: parse RR: %w", err)
			}
			offset = newOffset
			*sec.dest = append(*sec.dest, rr)
		}
	}

	return msg, nil
}

func parseRR(data []byte, offset int) (RR, int, error) {
	name, newOffset, err := decodeName(data, offset)
	if err != nil {
		return RR{}, 0, err
	}
	offset = newOffset
	if offset+10 > len(data) {
		return RR{}, 0, errors.New("dns: truncated RR")
	}

	rr := RR{
		Name:  name,
		Type:  binary.BigEndian.Uint16(data[offset : offset+2]),
		Class: binary.BigEndian.Uint16(data[offset+2 : offset+4]),
		TTL:   binary.BigEndian.Uint32(data[offset+4 : offset+8]),
	}
	rdLength := int(binary.BigEndian.Uint16(data[offset+8 : offset+10]))
	offset += 10

	if offset+rdLength > len(data) {
		return RR{}, 0, errors.New("dns: truncated RDATA")
	}

	rdataOffset := offset // exact byte position of RDATA within full packet
	offset += rdLength

	rr.RData, err = decodeRData(rr.Type, data, rdataOffset, rdLength)
	if err != nil {
		return RR{}, 0, err
	}

	return rr, offset, nil
}

// decodeRData decodes the RDATA section of a resource record.
// full is the entire DNS packet (needed for pointer decompression).
// rdataOffset is the exact byte position of RDATA within full.
// rdLen is the declared length of the RDATA field.
func decodeRData(rrType uint16, full []byte, rdataOffset, rdLen int) (string, error) {
	if rdataOffset+rdLen > len(full) {
		return "", errors.New("dns: rdata out of bounds")
	}
	rdata := full[rdataOffset : rdataOffset+rdLen]

	switch rrType {
	case TypeA:
		if len(rdata) != 4 {
			return "", errors.New("dns: invalid A record length")
		}
		return fmt.Sprintf("%d.%d.%d.%d", rdata[0], rdata[1], rdata[2], rdata[3]), nil

	case TypeAAAA:
		if len(rdata) != 16 {
			return "", errors.New("dns: invalid AAAA record length")
		}
		parts := make([]string, 8)
		for i := 0; i < 8; i++ {
			parts[i] = fmt.Sprintf("%x", binary.BigEndian.Uint16(rdata[i*2:i*2+2]))
		}
		return strings.Join(parts, ":"), nil

	case TypeNS, TypeCNAME:
		// Name may use pointer compression into earlier packet bytes — must decode
		// against the full packet starting at the exact rdata offset.
		name, _, err := decodeName(full, rdataOffset)
		if err != nil {
			return "", fmt.Errorf("dns: NS/CNAME name decode: %w", err)
		}
		return name, nil

	case TypeMX:
		if rdLen < 3 {
			return "", errors.New("dns: MX record too short")
		}
		pref := binary.BigEndian.Uint16(rdata[0:2])
		// Exchange name starts 2 bytes in; decode with pointer support.
		name, _, err := decodeName(full, rdataOffset+2)
		if err != nil {
			return "", fmt.Errorf("dns: MX exchange decode: %w", err)
		}
		return fmt.Sprintf("%d %s", pref, name), nil

	case TypeTXT:
		var parts []string
		i := 0
		for i < len(rdata) {
			l := int(rdata[i])
			i++
			if i+l > len(rdata) {
				break
			}
			parts = append(parts, string(rdata[i:i+l]))
			i += l
		}
		return strings.Join(parts, " "), nil

	case TypeSOA:
		// mname + rname + 5 x uint32 fields; decode names for display
		mname, off, err := decodeName(full, rdataOffset)
		if err != nil {
			return "", err
		}
		rname, _, err := decodeName(full, off)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s", mname, rname), nil

	default:
		return fmt.Sprintf("0x%x", rdata), nil
	}
}

// encodeName converts a domain name to DNS wire format labels.
func encodeName(domain string) ([]byte, error) {
	if domain == "." || domain == "" {
		return []byte{0}, nil
	}
	domain = strings.TrimSuffix(domain, ".")
	labels := strings.Split(domain, ".")
	var buf []byte
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return nil, fmt.Errorf("dns: invalid label %q", label)
		}
		buf = append(buf, byte(len(label)))
		buf = append(buf, []byte(label)...)
	}
	buf = append(buf, 0)
	return buf, nil
}

// decodeName reads a DNS name (with pointer compression) from data at offset.
func decodeName(data []byte, offset int) (string, int, error) {
	var labels []string
	visited := make(map[int]bool)
	startOffset := offset
	jumped := false
	finalOffset := 0

	for {
		if offset >= len(data) {
			return "", 0, errors.New("dns: name offset out of bounds")
		}
		if visited[offset] {
			return "", 0, errors.New("dns: pointer loop in name")
		}
		visited[offset] = true

		length := int(data[offset])

		if length == 0 {
			// End of name
			if !jumped {
				finalOffset = offset + 1
			}
			break
		}

		if length&0xC0 == 0xC0 {
			// Pointer
			if offset+1 >= len(data) {
				return "", 0, errors.New("dns: truncated pointer")
			}
			ptr := int(binary.BigEndian.Uint16(data[offset:offset+2]) & 0x3FFF)
			if !jumped {
				finalOffset = offset + 2
			}
			jumped = true
			offset = ptr
			continue
		}

		offset++
		if offset+length > len(data) {
			return "", 0, errors.New("dns: label extends beyond packet")
		}
		labels = append(labels, string(data[offset:offset+length]))
		offset += length
	}

	_ = startOffset
	if !jumped {
		// finalOffset already set above
	}
	name := strings.Join(labels, ".")
	return name, finalOffset, nil
}

func appendUint16(buf []byte, v uint16) []byte {
	return append(buf, byte(v>>8), byte(v))
}
