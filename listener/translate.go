// Copyright (c) 2016 Pani Networks
// All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// Package listener implements kubernetes API specific
// helper functions.
package listener

import (
	"fmt"
	"strings"
	"sync"

	"github.com/romana/core/common/api"
	"github.com/romana/core/common/client"
	"github.com/romana/core/common/log/trace"
	log "github.com/romana/rlog"

	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

type PolicyTranslator interface {
	Init(*client.Client, string, string)

	// Translates kubernetes policy into romana format.
	Kube2Romana(v1beta1.NetworkPolicy) (api.Policy, error)

	// Translates number of kubernetes policies into romana format.
	// Returns a list of translated policies, list of original policies
	// that failed to translate and an error.
	Kube2RomanaBulk([]v1beta1.NetworkPolicy) ([]api.Policy, []v1beta1.NetworkPolicy, error)
}

type Translator struct {
	client           *client.Client
	cacheMu          *sync.Mutex
	segmentLabelName string
	tenantLabelName  string
}

func (t *Translator) Init(client *client.Client, segmentLabelName, tenantLabelName string) {
	t.cacheMu = &sync.Mutex{}
	t.client = client
	t.segmentLabelName = segmentLabelName
	t.tenantLabelName = tenantLabelName
}

func (t Translator) GetClient() *client.Client {
	return t.client
}

// Kube2Romana reserved for future use.
func (t Translator) Kube2Romana(kubePolicy v1beta1.NetworkPolicy) (api.Policy, error) {
	return api.Policy{}, nil
}

// Kube2RomanaBulk attempts to translate a list of kubernetes policies into
// romana representation, returns a list of translated policies and a list
// of policies that can't be translated in original format.
func (t Translator) Kube2RomanaBulk(kubePolicies []v1beta1.NetworkPolicy) ([]api.Policy, []v1beta1.NetworkPolicy, error) {
	log.Debug("In Kube2RomanaBulk")
	var returnRomanaPolicy []api.Policy
	var returnKubePolicy []v1beta1.NetworkPolicy

	for kubePolicyNumber, _ := range kubePolicies {
		romanaPolicy, err := t.translateNetworkPolicy(&kubePolicies[kubePolicyNumber])
		if err != nil {
			log.Errorf("Error during policy translation %s", err)
			returnKubePolicy = append(returnKubePolicy, kubePolicies[kubePolicyNumber])
		} else {
			returnRomanaPolicy = append(returnRomanaPolicy, romanaPolicy)
		}
	}

	return returnRomanaPolicy, returnKubePolicy, nil

}

// translateNetworkPolicy translates a Kubernetes policy into
// Romana policy (see api.Policy) with the following rules:
// 1. Kubernetes Namespace corresponds to Romana Tenant
// 2. If Romana Tenant does not exist it is an error (a tenant should
//    automatically have been created when the namespace was added)
func (l *Translator) translateNetworkPolicy(kubePolicy *v1beta1.NetworkPolicy) (api.Policy, error) {
	policyID := getPolicyID(*kubePolicy)
	romanaPolicy := &api.Policy{Direction: api.PolicyDirectionIngress, ID: policyID}

	// Prepare translate group with original kubernetes policy and empty romana policy.
	translateGroup := &TranslateGroup{kubePolicy, romanaPolicy, TranslateGroupStartIndex}

	// Fill in AppliedTo field of romana policy.
	err := translateGroup.translateTarget(l)
	if err != nil {
		return *translateGroup.romanaPolicy, TranslatorError{ErrorTranslatingPolicyTarget, err}
	}

	// For each Ingress field in kubernetes policy, create Peer and Rule fields in
	// romana policy.
	for {
		err := translateGroup.translateNextIngress(l)
		if _, ok := err.(NoMoreIngressEntities); ok {
			break
		}

		if err != nil {
			return *translateGroup.romanaPolicy, TranslatorError{ErrorTranslatingPolicyIngress, err}
		}
	}

	return *translateGroup.romanaPolicy, nil
}

type TenantCacheEntry struct {
	Tenant api.Tenant
	//Segments []api.Segment
}

type TranslatorError struct {
	Code    TranslatorErrorType
	Details error
}

func (t TranslatorError) Error() string {
	return fmt.Sprintf("Translator error code %d, %s", t.Code, t.Details)
}

type TranslatorErrorType int

const (
	ErrorCacheUpdate TranslatorErrorType = iota
	ErrorTenantNotInCache
	ErrorTranslatingPolicyTarget
	ErrorTranslatingPolicyIngress
)

// TranslateGroup represent a state of translation of kubernetes policy
// into romana policy.
type TranslateGroup struct {
	kubePolicy   *v1beta1.NetworkPolicy
	romanaPolicy *api.Policy
	ingressIndex int
}

const TranslateGroupStartIndex = 0

// translateTarget analizes kubePolicy and fills romanaPolicy.AppliedTo field.
func (tg *TranslateGroup) translateTarget(translator *Translator) error {

	var targetEndpoint api.Endpoint

	// Translate kubernetes namespace into romana tenant. Must be defined.
	tenantID := GetTenantIDFromNamespaceName(tg.kubePolicy.ObjectMeta.Namespace)
	targetEndpoint.TenantID = tenantID

	// Empty PodSelector means policy applied to the entire namespace.
	if len(tg.kubePolicy.Spec.PodSelector.MatchLabels) == 0 {
		tg.romanaPolicy.AppliedTo = []api.Endpoint{targetEndpoint}

		log.Tracef(trace.Inside, "Segment was not specified in policy %v, assuming target is a namespace", tg.kubePolicy)
		return nil
	}

	// If PodSelector not specified assume tenant wide policy.
	kubeSegmentID, ok := tg.kubePolicy.Spec.PodSelector.MatchLabels[translator.segmentLabelName]
	if !ok || kubeSegmentID == "" {
		tg.romanaPolicy.AppliedTo = []api.Endpoint{targetEndpoint}
		log.Tracef(trace.Inside, "Segment was not specified in policy %v, assuming target is a namespace", tg.kubePolicy)
		return nil
	}

	targetEndpoint.SegmentID = kubeSegmentID

	tg.romanaPolicy.AppliedTo = []api.Endpoint{targetEndpoint}

	return nil
}

/// makeNextIngressPeer analyzes current Ingress rule and adds new Peer to romanaPolicy.Peers.
func (tg *TranslateGroup) makeNextIngressPeer(translator *Translator) error {
	ingress := tg.kubePolicy.Spec.Ingress[tg.ingressIndex]
	// romanaIngress := tg.romanaPolicy.Ingress[tg.ingressIndex]

	for _, fromEntry := range ingress.From {
		var sourceEndpoint api.Endpoint

		// This ingress field matching a namespace which will be our source tenant.
		if fromEntry.NamespaceSelector != nil {
			tenantID := GetTenantIDFromNamespaceName(fromEntry.NamespaceSelector.MatchLabels[translator.tenantLabelName])
			if tenantID == "" {
				// Use the namespace from objectmeta
				log.Infof("No label found for %s, using %s for tenant identifier", translator.tenantLabelName, tg.kubePolicy.ObjectMeta.Namespace)
				tenantID = tg.kubePolicy.ObjectMeta.Namespace
			}

			// Found a source tenant, let's register it as romana Peer.
			sourceEndpoint.TenantID = tenantID
		}

		// if source tenant not specified assume same as target tenant.
		if sourceEndpoint.TenantID == "" {
			sourceEndpoint.TenantID = GetTenantIDFromNamespaceName(tg.kubePolicy.ObjectMeta.Namespace)
		}

		// This ingress field matches a either one segment or all segments.
		if fromEntry.PodSelector != nil {

			// Get segment name from podSelector.
			kubeSegmentID, ok := fromEntry.PodSelector.MatchLabels[translator.segmentLabelName]
			if ok {
				// Register source tenant/segment as a romana Peer.
				sourceEndpoint.SegmentID = kubeSegmentID
			}
		}

		tg.romanaPolicy.Ingress[tg.ingressIndex].Peers = append(tg.romanaPolicy.Ingress[tg.ingressIndex].Peers, sourceEndpoint)

	}

	// kubernetes policy with empty Ingress with empty From field matches traffic
	// from all sources.
	if len(ingress.From) == 0 {
		tg.romanaPolicy.Ingress[tg.ingressIndex].Peers = append(tg.romanaPolicy.Ingress[tg.ingressIndex].Peers, api.Endpoint{Peer: api.Wildcard})

	}

	return nil
}

// makeNextRule analizes current ingress rule and adds a new Rule to romanaPolicy.Rules.
func (tg *TranslateGroup) makeNextRule(translator *Translator) error {
	ingress := tg.kubePolicy.Spec.Ingress[tg.ingressIndex]

	for _, toPort := range ingress.Ports {
		var proto string
		var ports []uint

		if toPort.Protocol == nil {
			proto = "tcp"
		} else {
			proto = strings.ToLower(string(*toPort.Protocol))
		}

		if toPort.Port == nil {
			ports = []uint{}
		} else {
			ports = []uint{uint(toPort.Port.IntValue())}
		}

		rule := api.Rule{Protocol: proto, Ports: ports}
		tg.romanaPolicy.Ingress[tg.ingressIndex].Rules = append(tg.romanaPolicy.Ingress[tg.ingressIndex].Rules, rule)
	}

	// treat policy with no rules as policy that targets all traffic.
	if len(ingress.Ports) == 0 {
		rule := api.Rule{Protocol: api.Wildcard}
		tg.romanaPolicy.Ingress[tg.ingressIndex].Rules = append(tg.romanaPolicy.Ingress[tg.ingressIndex].Rules, rule)
	}

	return nil
}

// translateNextIngress translates next Ingress object from kubePolicy into romanaPolicy
// Peer and Rule fields.
func (tg *TranslateGroup) translateNextIngress(translator *Translator) error {

	if tg.ingressIndex > len(tg.kubePolicy.Spec.Ingress)-1 {
		return NoMoreIngressEntities{}
	}

	tg.romanaPolicy.Ingress = append(tg.romanaPolicy.Ingress, api.RomanaIngress{})

	// Translate Ingress.From into romanaPolicy.ToPorts.
	err := tg.makeNextIngressPeer(translator)
	if err != nil {
		return err
	}

	// Translate Ingress.Ports into romanaPolicy.Rules.
	err = tg.makeNextRule(translator)
	if err != nil {
		return err
	}

	tg.ingressIndex++

	return nil
}

// NoMoreIngressEntities is an error that indicates that translateNextIngress
// went through all Ingress entries in TranslateGroup.kubePolicy.
type NoMoreIngressEntities struct{}

func (e NoMoreIngressEntities) Error() string {
	return "Done translating"
}
