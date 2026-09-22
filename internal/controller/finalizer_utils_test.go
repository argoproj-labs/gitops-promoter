/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("removeUnreferencedSecretFinalizers", func() {
	const (
		ns        = "test-ns"
		finalizer = promoterv1alpha1.ScmProviderSecretFinalizer
	)

	secret := func(name string) *v1.Secret {
		return &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Finalizers: []string{finalizer}}}
	}

	It("keeps cleaning up the other Secrets when one update fails", func() {
		ctx := context.Background()
		c := fake.NewClientBuilder().
			WithScheme(utils.GetScheme()).
			WithObjects(secret("a-stuck"), secret("b-stale"), secret("c-referenced")).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if obj.GetName() == "a-stuck" {
						return errors.New("simulated update failure")
					}
					return cl.Update(ctx, obj, opts...)
				},
			}).
			Build()

		err := removeUnreferencedSecretFinalizers(ctx, c, ns, finalizer, map[string]bool{"c-referenced": true})
		Expect(err).To(MatchError(ContainSubstring("simulated update failure")))

		finalizersOf := func(name string) []string {
			var s v1.Secret
			Expect(c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &s)).To(Succeed())
			return s.Finalizers
		}
		Expect(finalizersOf("a-stuck")).To(ContainElement(finalizer))
		Expect(finalizersOf("b-stale")).ToNot(ContainElement(finalizer))
		Expect(finalizersOf("c-referenced")).To(ContainElement(finalizer))
	})
})
