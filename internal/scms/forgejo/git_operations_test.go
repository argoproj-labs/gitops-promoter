package forgejo

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GitAuthenticationProvider", func() {
	newProvider := func(data map[string][]byte) GitAuthenticationProvider {
		return GitAuthenticationProvider{secret: &k8sV1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds"}, Data: data}}
	}

	It("uses oauth2 and the token when the secret has a token", func() {
		gap := newProvider(map[string][]byte{"token": []byte("s3cret")})
		user, err := gap.GetUser(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(user).To(Equal("oauth2"))
		token, err := gap.GetToken(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(Equal("s3cret"))
	})

	It("uses the username and password when the secret has basic auth credentials", func() {
		gap := newProvider(map[string][]byte{"username": []byte("alice"), "password": []byte("pw")})
		user, err := gap.GetUser(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(user).To(Equal("alice"))
		token, err := gap.GetToken(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(token).To(Equal("pw"))
	})

	It("returns an error when the secret has no credentials", func() {
		gap := newProvider(map[string][]byte{})
		_, err := gap.GetUser(context.Background())
		Expect(err).To(HaveOccurred())
		_, err = gap.GetToken(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
