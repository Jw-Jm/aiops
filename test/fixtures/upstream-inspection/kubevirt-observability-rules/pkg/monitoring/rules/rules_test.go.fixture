/*
This file is part of the KubeVirt project

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

Copyright The KubeVirt Authors.
*/

package rules

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRules(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Rules Suite")
}

var _ = Describe("Rules Setup", func() {
	BeforeEach(func() {
		ResetRegistry()
	})

	It("should register all rules and alerts", func() {
		err := SetupRules("kubevirt", nil, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should build a valid PrometheusRule", func() {
		err := SetupRules("kubevirt", nil, nil)
		Expect(err).ToNot(HaveOccurred())

		pr, err := BuildPrometheusRule(
			"kubevirt-observability-rules",
			"kubevirt",
			map[string]string{"app.kubernetes.io/managed-by": "kubevirt-observability-controller"},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(pr).ToNot(BeNil())
		Expect(pr.Spec.Groups).ToNot(BeEmpty())
		Expect(pr.Name).To(Equal("kubevirt-observability-rules"))
		Expect(pr.Namespace).To(Equal("kubevirt"))
		Expect(pr.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "kubevirt-observability-controller"))
	})

	Context("allowlist filtering", func() {
		It("should include only allowlisted alerts and recording rules", func() {
			alertsAllowlist := map[string]bool{
				"VirtAPIDown": true,
			}
			recordingRulesAllowlist := map[string]bool{
				"cluster:kubevirt_virt_api_up:sum": true,
			}

			err := SetupRules("kubevirt", alertsAllowlist, recordingRulesAllowlist)
			Expect(err).ToNot(HaveOccurred())

			pr, err := BuildPrometheusRule(
				"kubevirt-observability-rules",
				"kubevirt",
				map[string]string{"app.kubernetes.io/managed-by": "kubevirt-observability-controller"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(pr).ToNot(BeNil())

			var alertCount, recordingRuleCount int
			for _, g := range pr.Spec.Groups {
				for _, r := range g.Rules {
					if r.Alert != "" {
						alertCount++
					} else {
						recordingRuleCount++
					}
				}
			}

			Expect(alertCount).To(Equal(1))
			Expect(recordingRuleCount).To(Equal(1))
		})

		It("should report no registered rules when both allowlists are empty maps", func() {
			err := SetupRules("kubevirt", map[string]bool{}, map[string]bool{})
			Expect(err).ToNot(HaveOccurred())
			Expect(HasRegisteredRules()).To(BeFalse())
		})
	})
})
