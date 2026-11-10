// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

const policy = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: web
  namespace: shop
spec:
  podSelector: {}
  ingress:
    - from:
        - podSelector: {}
    - ports:
        - port: 80
  egress:
    - to:
        - ipBlock:
            cidr: 10.0.0.0/8
    - to:
        - podSelector: {}
        - ipBlock:
            cidr: 0.0.0.0/0
`

// The NetworkPolicy rules name the rule and the peer that fail, by index.
func TestNetpolRulesNameTheFailingRule(t *testing.T) {
	result := celruletest.Eval(t, celNoAllowAllIngress, policy)
	celruletest.Expect(t, result, "spec.ingress.1.from")
	celruletest.ExpectResource(t, result, "shop/web", "networking.k8s.io/v1/NetworkPolicy")

	celruletest.Expect(t, celruletest.Eval(t, celNoWideCidrEgress, policy), "spec.egress.1.to.1.ipBlock.cidr")
	// 0.0.0.0/0 contains the metadata IP, and nothing excepts it.
	celruletest.Expect(t, celruletest.Eval(t, celNoMetadataEgress, policy), "spec.egress.1.to.1.ipBlock.except")

	excepted := `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: web
spec:
  podSelector: {}
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: ["169.254.169.254/32"]
`
	celruletest.Expect(t, celruletest.Eval(t, celNoMetadataEgress, excepted))
	celruletest.Expect(t, celruletest.Eval(t, celNoAllowAllIngress, excepted))

	annotated := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  annotations:\n    netpol.confighub.com/ingress-coverage: uncovered\n"
	celruletest.Expect(t, celruletest.Eval(t, celNoCoverageFinding, annotated), "metadata.annotations.netpol~1confighub~1com/ingress-coverage")
	celruletest.Expect(t, celruletest.Eval(t, celNoWideCidrEgress, annotated))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}
