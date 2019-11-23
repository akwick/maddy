package dns

import (
	"net"
	"testing"

	"github.com/emersion/go-smtp"
	"github.com/foxcpp/go-mockdns"
	"github.com/foxcpp/maddy/check"
	"github.com/foxcpp/maddy/future"
	"github.com/foxcpp/maddy/module"
	"github.com/foxcpp/maddy/testutils"
)

func TestRequireMatchingRDNS(t *testing.T) {
	test := func(rdns string, srcHost string, fail bool) {
		rdnsFut := future.New()
		var ptr []string
		if rdns != "" {
			rdnsFut.Set(rdns)
			ptr = []string{rdns}
		} else {
			rdnsFut.Set(nil)
		}

		res := requireMatchingRDNS(check.StatelessCheckContext{
			Resolver: &mockdns.Resolver{
				Zones: map[string]mockdns.Zone{
					"4.3.2.1.in-addr.arpa.": mockdns.Zone{
						PTR: ptr,
					},
				},
			},
			MsgMeta: &module.MsgMetadata{
				Conn: &module.ConnState{
					ConnectionState: smtp.ConnectionState{
						RemoteAddr: &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 55555},
						Hostname:   srcHost,
					},
					RDNSName: rdnsFut,
				},
			},
			Logger: testutils.Logger(t, "require_matching_rdns"),
		})

		actualFail := res.Reason != nil
		if fail && !actualFail {
			t.Errorf("%v, %s: expected failure but check succeeded", rdns, srcHost)
		}
		if !fail && actualFail {
			t.Errorf("%v, %s: unexpected failure", rdns, srcHost)
		}
	}

	test("", "example.org", true)
	test("example.org", "[1.2.3.4]", true)
	test("example.org", "[IPv6:beef::1]", true)
	test("example.org", "example.org", false)
	test("example.org.", "example.org", false)
	test("example.org", "example.org.", false)
	test("example.org.", "example.org.", false)
	test("example.com.", "example.org.", true)
}

func TestRequireMXRecord(t *testing.T) {
	test := func(mailFrom, mxDomain string, mx []net.MX, fail bool) {
		res := requireMXRecord(check.StatelessCheckContext{
			Resolver: &mockdns.Resolver{
				Zones: map[string]mockdns.Zone{
					mxDomain + ".": mockdns.Zone{
						MX: mx,
					},
				},
			},
			MsgMeta: &module.MsgMetadata{
				Conn: &module.ConnState{
					ConnectionState: smtp.ConnectionState{
						RemoteAddr: &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 55555},
					},
				},
			},
			Logger: testutils.Logger(t, "require_mx_record"),
		}, mailFrom)

		actualFail := res.Reason != nil
		if fail && !actualFail {
			t.Errorf("%v, %v: expected failure but check succeeded", mailFrom, mx)
		}
		if !fail && actualFail {
			t.Errorf("%v, %v: unexpected failure", mailFrom, mx)
		}
	}

	test("foo@example.org", "example.org", nil, true)
	test("foo@example.com", "", nil, true) // NXDOMAIN
	test("foo@[1.2.3.4]", "", nil, true)
	test("[IPv6:beef::1]", "", nil, true)
	test("[IPv6:beef::1]", "", nil, true)
	test("foo@example.org", "example.org", []net.MX{{Host: "a.com"}}, false)
	test("foo@", "", nil, true)
	test("", "", nil, false) // Permit <> for bounces.

	// TODO: Deny this.
	test("foo@example.org", "example.org", []net.MX{{Host: "."}}, false)
}

func TestMatchingEHLO(t *testing.T) {
	test := func(srcHost string, srcIP net.IP, a, aaaa []string, fail bool) {
		zones := map[string]mockdns.Zone{}
		if a != nil && aaaa != nil {
			zones[srcHost+"."] = mockdns.Zone{
				A:    a,
				AAAA: aaaa,
			}
		}

		res := requireMatchingEHLO(check.StatelessCheckContext{
			Resolver: &mockdns.Resolver{
				Zones: zones,
			},
			MsgMeta: &module.MsgMetadata{
				Conn: &module.ConnState{
					ConnectionState: smtp.ConnectionState{
						RemoteAddr: &net.TCPAddr{IP: srcIP, Port: 55555},
						Hostname:   srcHost,
					},
				},
			},
			Logger: testutils.Logger(t, "require_matching_helo"),
		})

		actualFail := res.Reason != nil
		if fail && !actualFail {
			t.Errorf("srcHost %v, srcIP %v, a %v, aaaa %v: expected failure but check succeeded", srcHost, srcIP, a, aaaa)
		}
		if !fail && actualFail {
			t.Errorf("srcHost %v, srcIP %v, a %v, aaaa %v: unexpected failure", srcHost, srcIP, a, aaaa)
		}
	}

	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		nil, nil, true)
	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		[]string{}, []string{}, true)
	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		[]string{"2.3.4.5"}, nil, true)
	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		[]string{"2.3.4.5"}, []string{"beef::1"}, true)
	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		[]string{"2.3.4.5"}, []string{"beef::1"}, true)
	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		[]string{"1.2.3.4"}, nil, true)
	test("mx.example.org", net.IPv4(1, 2, 3, 4),
		[]string{"1.2.3.4"}, []string{"beef::1"}, false)
	test("[1.2.3.5]", net.IPv4(1, 2, 3, 4),
		nil, nil, true)
	test("[not valid]", net.IPv4(1, 2, 3, 4),
		nil, nil, true)
	test("[1.2.3.4]", net.IPv4(1, 2, 3, 4),
		nil, nil, false)
	test("[IPv6:beef::1]", net.IPv4(1, 2, 3, 4),
		nil, nil, true)
	test("[IPv6:NOT VALID]", net.IPv4(1, 2, 3, 4),
		nil, nil, true)
	test("[IPv6:beef::1]", net.ParseIP("beef::2"),
		nil, nil, true)
	test("[IPv6:beef::1]", net.ParseIP("beef::1"),
		nil, nil, false)
}
