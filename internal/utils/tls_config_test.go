package utils

import (
	"crypto/tls"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TLS config", func() {
	DescribeTable("ParseTLSVersion",
		func(input string, want uint16, wantErr bool) {
			got, err := ParseTLSVersion(input)
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("empty means default", "", uint16(0), false),
		Entry("VersionTLS12", "VersionTLS12", uint16(tls.VersionTLS12), false),
		Entry("VersionTLS13", "VersionTLS13", uint16(tls.VersionTLS13), false),
		Entry("VersionTLS11 rejected", "VersionTLS11", uint16(0), true),
		Entry("VersionTLS10 rejected", "VersionTLS10", uint16(0), true),
		Entry("short form rejected", "1.2", uint16(0), true),
		Entry("unsupported", "VersionTLS99", uint16(0), true),
		Entry("garbage", "ssl3", uint16(0), true),
	)

	Describe("ParseTLSCiphers", func() {
		var name1, name2, tls13OnlyName string
		var id1, id2 uint16

		BeforeEach(func() {
			tls12Suites, tls13Name := splitCipherSuites()
			Expect(len(tls12Suites)).To(BeNumerically(">=", 2))
			Expect(tls13Name).NotTo(BeEmpty())
			name1, id1 = tls12Suites[0].Name, tls12Suites[0].ID
			name2, id2 = tls12Suites[1].Name, tls12Suites[1].ID
			tls13OnlyName = tls13Name
		})

		It("accepts an empty list", func() {
			got, err := ParseTLSCiphers(nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeNil())
		})

		It("accepts one TLS 1.2 suite", func() {
			got, err := ParseTLSCiphers([]string{name1})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal([]uint16{id1}))
		})

		It("accepts multiple TLS 1.2 suites", func() {
			got, err := ParseTLSCiphers([]string{name1, name2})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal([]uint16{id1, id2}))
		})

		It("rejects an unknown suite", func() {
			_, err := ParseTLSCiphers([]string{"TLS_NOT_A_REAL_CIPHER"})
			Expect(err).To(HaveOccurred())
		})

		It("rejects a TLS 1.3 suite", func() {
			_, err := ParseTLSCiphers([]string{tls13OnlyName})
			Expect(err).To(MatchError(ContainSubstring("TLS 1.3 cipher suites are not configurable in Go")))
		})

		It("rejects RC4", func() {
			_, err := ParseTLSCiphers([]string{"TLS_RSA_WITH_RC4_128_SHA"})
			Expect(err).To(MatchError(ContainSubstring(`insecure cipher suite "TLS_RSA_WITH_RC4_128_SHA"`)))
		})

		It("rejects 3DES", func() {
			_, err := ParseTLSCiphers([]string{"TLS_RSA_WITH_3DES_EDE_CBC_SHA"})
			Expect(err).To(MatchError(ContainSubstring(`insecure cipher suite "TLS_RSA_WITH_3DES_EDE_CBC_SHA"`)))
		})

		It("rejects a secure suite listed with RC4", func() {
			_, err := ParseTLSCiphers([]string{name1, "TLS_RSA_WITH_RC4_128_SHA"})
			Expect(err).To(MatchError(ContainSubstring("insecure cipher suite")))
		})
	})

	DescribeTable("ParseTLSCurvePreferences",
		func(input []int32, want []tls.CurveID, wantErr bool) {
			got, err := ParseTLSCurvePreferences(input)
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("empty", nil, nil, false),
		Entry("X25519", []int32{int32(tls.X25519)}, []tls.CurveID{tls.X25519}, false),
		Entry("X25519MLKEM768 with classical fallback",
			[]int32{int32(tls.X25519MLKEM768), int32(tls.X25519)},
			[]tls.CurveID{tls.X25519MLKEM768, tls.X25519},
			false,
		),
		Entry("P-256 and P-384", []int32{23, 24}, []tls.CurveID{tls.CurveP256, tls.CurveP384}, false),
		Entry("unsupported", []int32{99999}, nil, true),
		Entry("duplicate", []int32{29, 29}, nil, true),
	)

	Describe("ValidateTLSConfig", func() {
		var cipherTLS12Only uint16

		BeforeEach(func() {
			found := false
			for _, cs := range tls.CipherSuites() {
				supports12 := false
				supports13 := false
				for _, v := range cs.SupportedVersions {
					if v == tls.VersionTLS12 {
						supports12 = true
					}
					if v == tls.VersionTLS13 {
						supports13 = true
					}
				}
				if supports12 && !supports13 {
					cipherTLS12Only = cs.ID
					found = true
					break
				}
			}
			if !found {
				Skip("no TLS 1.2-only cipher suite available")
			}
		})

		It("accepts a TLS 1.2 cipher at the TLS 1.2 floor", func() {
			Expect(ValidateTLSConfig(tls.VersionTLS12, []uint16{cipherTLS12Only})).To(Succeed())
		})

		It("treats an unset minimum as TLS 1.2", func() {
			Expect(ValidateTLSConfig(0, []uint16{cipherTLS12Only})).To(Succeed())
		})

		It("rejects a TLS 1.2 cipher when the minimum is TLS 1.3", func() {
			Expect(ValidateTLSConfig(tls.VersionTLS13, []uint16{cipherTLS12Only})).NotTo(Succeed())
		})
	})

	Describe("BuildTLSConfig", func() {
		It("returns nil when nothing is configured", func() {
			cfg, warnings, err := TLSSettings{}.BuildTLSConfig()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).To(BeNil())
			Expect(warnings).To(BeNil())
		})

		It("applies a minimum version and curve preferences", func() {
			cfg, warnings, err := TLSSettings{
				MinVersion:       "VersionTLS12",
				CurvePreferences: []int32{int32(tls.X25519), int32(tls.CurveP256)},
			}.BuildTLSConfig()
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())
			Expect(cfg.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
			Expect(cfg.CurvePreferences).To(Equal([]tls.CurveID{tls.X25519, tls.CurveP256}))
		})

		It("ignores ciphers when the minimum is TLS 1.3", func() {
			tls12Suites, _ := splitCipherSuites()
			Expect(tls12Suites).NotTo(BeEmpty())
			cfg, warnings, err := TLSSettings{
				MinVersion:   "VersionTLS13",
				CipherSuites: []string{tls12Suites[0].Name},
			}.BuildTLSConfig()
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(HaveLen(1))
			Expect(cfg.CipherSuites).To(BeEmpty())
		})

		It("rejects a TLS 1.3 cipher when no minimum is set", func() {
			_, tls13Name := splitCipherSuites()
			Expect(tls13Name).NotTo(BeEmpty())
			_, _, err := TLSSettings{CipherSuites: []string{tls13Name}}.BuildTLSConfig()
			Expect(err).To(MatchError(ContainSubstring("TLS 1.3 cipher suites are not configurable in Go")))
		})

		It("rejects a short-form minimum version", func() {
			_, _, err := TLSSettings{MinVersion: "1.2"}.BuildTLSConfig()
			Expect(err).To(HaveOccurred())
		})

		It("rejects TLS 1.0 with RC4 and 3DES", func() {
			_, _, err := TLSSettings{
				MinVersion:   "VersionTLS10",
				CipherSuites: []string{"TLS_RSA_WITH_RC4_128_SHA", "TLS_RSA_WITH_3DES_EDE_CBC_SHA"},
			}.BuildTLSConfig()
			Expect(err).To(MatchError(ContainSubstring("unsupported TLS version")))
		})

		It("rejects RC4 at TLS 1.2", func() {
			_, _, err := TLSSettings{
				MinVersion:   "VersionTLS12",
				CipherSuites: []string{"TLS_RSA_WITH_RC4_128_SHA"},
			}.BuildTLSConfig()
			Expect(err).To(MatchError(ContainSubstring(`insecure cipher suite "TLS_RSA_WITH_RC4_128_SHA"`)))
		})
	})

	Describe("ConfigurableTLSCipherNames", func() {
		It("lists only suites ParseTLSCiphers accepts", func() {
			names := ConfigurableTLSCipherNames()
			Expect(names).NotTo(BeEmpty())
			for _, name := range names {
				_, err := ParseTLSCiphers([]string{name})
				Expect(err).NotTo(HaveOccurred(), name)
			}
			for _, blocked := range []string{
				"TLS_AES_128_GCM_SHA256",
				"TLS_AES_256_GCM_SHA384",
				"TLS_CHACHA20_POLY1305_SHA256",
				"TLS_RSA_WITH_RC4_128_SHA",
				"TLS_RSA_WITH_3DES_EDE_CBC_SHA",
			} {
				Expect(names).NotTo(ContainElement(blocked))
			}
		})
	})

	It("names TLS versions", func() {
		Expect(TLSVersionName(tls.VersionTLS12)).To(Equal("VersionTLS12"))
		Expect(TLSVersionName(0)).To(Equal("unknown (0)"))
	})

	Describe("process TLS policy", Serial, func() {
		It("installs and clears settings", func() {
			DeferCleanup(ResetProcessTLSSettingsForTest)
			ResetProcessTLSSettingsForTest()

			empty := &tls.Config{}
			ApplyProcessTLSConfig(empty)
			Expect(empty.MinVersion).To(Equal(uint16(0)))
			Expect(empty.CipherSuites).To(BeEmpty())
			Expect(empty.CurvePreferences).To(BeEmpty())

			warnings, err := SetProcessTLSSettings(TLSSettings{
				MinVersion:       "VersionTLS13",
				CurvePreferences: []int32{int32(tls.X25519)},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(warnings).To(BeEmpty())

			cfg := &tls.Config{}
			ApplyProcessTLSConfig(cfg)
			Expect(cfg.MinVersion).To(Equal(uint16(tls.VersionTLS13)))
			Expect(cfg.CurvePreferences).To(Equal([]tls.CurveID{tls.X25519}))

			_, err = SetProcessTLSSettings(TLSSettings{})
			Expect(err).NotTo(HaveOccurred())
			cleared := &tls.Config{MinVersion: tls.VersionTLS12}
			ApplyProcessTLSConfig(cleared)
			Expect(cleared.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
		})
	})
})

func splitCipherSuites() (tls12 []*tls.CipherSuite, tls13OnlyName string) {
	for _, cs := range tls.CipherSuites() {
		if cipherSupportsVersionAtMost(cs, tls.VersionTLS12) {
			tls12 = append(tls12, cs)
			continue
		}
		if tls13OnlyName == "" {
			tls13OnlyName = cs.Name
		}
	}
	return tls12, tls13OnlyName
}

func cipherSupportsVersionAtMost(cs *tls.CipherSuite, highest uint16) bool {
	for _, v := range cs.SupportedVersions {
		if v <= highest {
			return true
		}
	}
	return false
}
