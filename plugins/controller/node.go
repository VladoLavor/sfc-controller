// Copyright (c) 2017 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"fmt"
	"net"

	"github.com/ligato/sfc-controller/plugins/controller/model"
	"github.com/ligato/sfc-controller/plugins/controller/vppagentapi"
	"github.com/ligato/vpp-agent/plugins/defaultplugins/common/model/l2"
)

// The node module support CRUD operations.  As nodes are added to the system
// the appropriate interfaces are created for this node and any routes are
// also created.  If a node is deleted from the system, then cleanup is to be
// done in ETCD.

func (s *Plugin) findInterfaceInNode(nodeName string, ifName string) (*controller.Interface, string) {

	n, exists := s.ramConfigCache.Nodes[nodeName]
	if !exists {
		return nil, ""
	}
	for _, iFace := range n.Interfaces {
		if iFace.Name == ifName {
			return iFace, iFace.IfType
		}
	}

	return nil, ""
}

func (s *Plugin) findInterfacesForThisLabelInNode(nodeName string, labels []string) ([]*controller.Interface, []string) {

	var interfaces []*controller.Interface
	var ifTypes []string

	n, exists := s.ramConfigCache.Nodes[nodeName]
	if !exists {
		return interfaces, ifTypes
	}
	for _, iFace := range n.Interfaces {
		for _, ifaceLabel := range iFace.CustomLabels{
			for _, label := range labels {
				if ifaceLabel == label {
					interfaces = append(interfaces, iFace)
					ifTypes = append(ifTypes, iFace.IfType)
				}
			}
		}
	}

	return interfaces, ifTypes
}

// nodeValidateInterfaces validates all the fields
func (s *Plugin) nodeValidateInterfaces(nodeName string, iFaces []*controller.Interface) error {

	for _, iFace := range iFaces {
		switch iFace.IfType {
		case controller.IfTypeEthernet:
		case controller.IfTypeVxlanTunnel:
		default:
			return fmt.Errorf("node/if: %s/%s has invalid if type '%s'",
				nodeName, iFace.Name, iFace.IfType)
		}
		for _, ipAddress := range iFace.IpAddresses {
			ip, network, err := net.ParseCIDR(ipAddress)
			if err != nil {
				return fmt.Errorf("node/if: %s/%s '%s', expected format i.p.v.4/xx, or ip::v6/xx",
					nodeName, iFace.Name, err)
			}
			log.Debugf("nodeValidateInterfaces: ip: %s, network: %s", ip, network)
		}
		if iFace.IpAddresses == nil {
			log.Warnf("nodeValidateInterfaces: node/if: %s/%s, missing ipaddress",
				nodeName, iFace.Name)
		}
	}

	return nil
}

// FindVxlanIPaddress looks up the vxlan ip address for this node in the if list
func (s *Plugin) FindVxlanIPaddress(nodeName string) (string, error) {

	n, exists := s.ramConfigCache.Nodes[nodeName]
	if !exists {
		return "", fmt.Errorf("no vxlan ip address configured for node: %s", nodeName)
	}
	for _, iFace := range n.Interfaces {
		switch iFace.IfType {
		case controller.IfTypeVxlanTunnel:
			for _, ipAddress := range iFace.IpAddresses {
				ip, _, err := net.ParseCIDR(ipAddress)
				if err == nil {
					log.Debugf("FindVxlanIPaddress: node: %s, found vxlan iupaddr: %s",
						nodeName, ip)
					return ip.String(), nil
				}
			}
		}
	}

	return "", fmt.Errorf("no vxlan ip address found for node: %s", nodeName)
}

// NodeValidate validates all the fields
func (s *Plugin) NodeValidate(n *controller.Node) error {

	if n.Interfaces != nil && n.Vswitches != nil {
		return fmt.Errorf("node: %s can only model 1 vswitch with interfaces, or multiple with vswitches, but not both",
			n.Name)
	} else if n.HasMultipleVswitches && n.Interfaces != nil {
		return fmt.Errorf("node: %s indicating multiple vswitches but using the interfaces array, use vnfs instead",
			n.Name)
	} else if !n.HasMultipleVswitches && n.Vswitches != nil {
		return fmt.Errorf("node: %s indicating single vswitch but using the vnf's array, use interfaces instead",
			n.Name)
	}

	if n.Interfaces != nil {
		if err := s.nodeValidateInterfaces(n.Name, n.Interfaces); err != nil {
			return err
		}
	}

	if n.Vswitches != nil {
		for _, vnf := range n.Vswitches {
			if vnf.Name == "" {
				return fmt.Errorf("node: %s has missing vnf name", n.Name)
			}
			switch vnf.VnfType {
			case controller.VNFTypeVPPVswitch:
			case controller.VNFTypeExternal:
			default:
				return fmt.Errorf("vnf: %s has invalid vnf type '%s'",
					vnf.Name, vnf.VnfType)
			}

			if err := s.nodeValidateInterfaces(vnf.Name, vnf.Interfaces); err != nil {
				return err
			}
		}
	}

	for _, l2bd := range n.L2Bds {

		if l2bd.L2BdTemplate != "" && l2bd.BdParms != nil {
			return fmt.Errorf("node: %s, l2bd: %s  cannot refer to temmplate and provide l2bd parameters",
				n.Name, l2bd.Name)
		}
		if l2bd.L2BdTemplate != "" {
			if l2bdt := s.FindL2BDTemplate(l2bd.L2BdTemplate); l2bdt == nil {
				return fmt.Errorf("node: %s, l2bd: %s  has invalid reference to non-existant l2bd template '%s'",
					n.Name, l2bd.Name, l2bd.L2BdTemplate)
			}
		}
	}

	return nil
}

func vppKeyL2BDName(nodeName, l2bdName string) string {
	return "L2BD_" + nodeName + "_" + l2bdName
}

// RenderNodeL2BDs process the node's bridges
func (s *Plugin) RenderNodeL2BDs(n *controller.Node,
	vppAgent string,
	l2bds []*controller.L2BD,
	nodeState *controller.NodeState) error {

	var bdParms *controller.BDParms

	for _, l2bd := range l2bds {

		if l2bd.L2BdTemplate != "" {
			bdParms = s.FindL2BDTemplate(l2bd.L2BdTemplate)
		} else {
			if l2bd.BdParms == nil {
				bdParms = s.GetDefaultSystemBDParms()
			} else {
				bdParms = l2bd.BdParms
			}
		}
		vppKV := vppagentapi.ConstructL2BD(
			vppAgent,
			vppKeyL2BDName(n.Name, l2bd.Name),
			nil,
			bdParms)
		nodeState.RenderedVppAgentEntries =
			s.ConfigTransactionAddVppEntry(nodeState.RenderedVppAgentEntries, vppKV)

		log.Infof("RenderNodeL2BDs: vswitch: %s, vppKV: %v", vppAgent, vppKV)
	}

	return nil
}

// RenderNodeInterfaces process the node's interfaces
func (s *Plugin) RenderNodeInterfaces(n *controller.Node,
	vppAgent string,
	iFaces []*controller.Interface,
	nodeState *controller.NodeState) error {

	var vppKV *vppagentapi.KeyValueEntryType

	for _, iFace := range iFaces {
		switch iFace.IfType {
		case controller.IfTypeEthernet:
			if ! iFace.RenderingNotRequired {
				vppKV = vppagentapi.ConstructEthernetInterface(
					vppAgent,
					iFace.Name,
					iFace.IpAddresses,
					iFace.MacAddress,
					s.ResolveMtu(iFace.Mtu),
					iFace.AdminStatus,
					s.ResolveRxMode(iFace.RxMode))
				nodeState.RenderedVppAgentEntries =
					s.ConfigTransactionAddVppEntry(nodeState.RenderedVppAgentEntries, vppKV)
			}
		}
		log.Infof("RenderNodeInterfaces: vswitch: %s, ifType: %s, vppKV: %v",
			vppAgent, iFace.IfType, vppKV)
	}

	return nil
}

// NodeCreate add to ram cache and run topology side effects
func (s *Plugin) NodeCreate(n *controller.Node, render bool) error {

	if err := s.NodeValidate(n); err != nil {
		return err
	}

	if render {
		if err := s.RenderNode(n); err != nil {
			return err
		}
	}

	if err := s.NodeWriteToDatastore(n); err != nil {
		return err
	}
	s.ramConfigCache.Nodes[n.Name] = *n

	// inform ipam pool that a new node might need a node scope pool allocated
	s.IPAMPoolEntityCreate(n.Name)

	return nil
}

// RenderNode effect interface ... processing
func (s *Plugin) RenderNode(n *controller.Node) error {

	if ns, exists := s.ramConfigCache.NodeState[n.Name]; exists {
		// add the current rendered etc keys to the before config transaction
		s.CopyRenderedVppAgentEntriesToBeforeConfigTransaction(ns.RenderedVppAgentEntries)
	}

	// clear the ram cache entry, add state to it as we render, then add it to
	// the datastore?
	delete(s.ramConfigCache.NodeState, n.Name)
	nodeState := &controller.NodeState{}

	defer s.RenderNodeCleanup(n, nodeState)

	// create interfaces in the single/only vswitch
	if n.Interfaces != nil {
		if err := s.RenderNodeInterfaces(n, n.Name, n.Interfaces, nodeState); err != nil {
			return err
		}
	}
	// create interfaces in the vswitches
	for _, vnf := range n.Vswitches {
		if err := s.RenderNodeInterfaces(n, vnf.Name, vnf.Interfaces, nodeState); err != nil {
			return err
		}
	}

	// create l2bds
	if n.L2Bds != nil {
		if err := s.RenderNodeL2BDs(n, n.Name, n.L2Bds, nodeState); err != nil {
			return err
		}
	}

	return nil
}

// RenderNodeCleanup cleansup if necessary and write the state to the db
func (s *Plugin) RenderNodeCleanup(n *controller.Node, nodeState *controller.NodeState) error {

	if len(nodeState.Msg) == 0 {
		s.AppendStatusMsgToNodeState("OK", nodeState)
		nodeState.OperStatus = controller.VNFServiceOperStatusUp
	} else {
		s.ConfigCleanupErrorOcurredDuringRendering()
		nodeState.RenderedVppAgentEntries = nil
		nodeState.OperStatus = controller.VNFServiceOperStatusDown
	}

	nodeState.Name = n.Name
	s.ramConfigCache.NodeState[n.Name] = nodeState

	if err := s.NodeStateWriteToDatastore(nodeState); err != nil {
		return err
	}

	log.Debugf("RenderNodeCleanup: node:%v, status=%v", n, nodeState)

	return nil
}

// AppendStatusMsgToNodeState appends a msg for a vnf-service for state information
func (s *Plugin) AppendStatusMsgToNodeState(msg string, nodeState *controller.NodeState) {
	nodeState.Msg = append(nodeState.Msg, msg)
}

// NodeDelete removes the node from the system
func (s *Plugin) NodeDelete(nodeName string) error {

	if _, exists := s.ramConfigCache.Nodes[nodeName]; !exists {
		return nil
	}
	delete(s.ramConfigCache.Nodes, nodeName)
	s.DeleteFromDatastore(controller.NodeNameKey(nodeName))

	// after the transaction, nothing will have been added to so all the old
	// entries will be removed
	if ns, exists := s.ramConfigCache.NodeState[nodeName]; exists {
		// add the current rendered etc keys to the before config transaction
		s.CopyRenderedVppAgentEntriesToBeforeConfigTransaction(ns.RenderedVppAgentEntries)
	}
	delete(s.ramConfigCache.NodeState, nodeName)
	s.DeleteFromDatastore(controller.NodeStatusNameKey(nodeName))

	if err := s.VNFServicesRender(); err != nil {
		return err
	}

	s.IPAMPoolEntityDelete(nodeName)

	return nil
}

// NodesRender adapts to resource changes
func (s *Plugin) NodesRender() error {

	// traverse each service, and render segments if possible
	for _, n := range s.ramConfigCache.Nodes {
		log.Debugf("NodesRender: node: ", n)
		if err := s.RenderNode(&n); err != nil {
			return err
		}
	}
	return nil
}

// FindL2BDForNode by name
func (s *Plugin) FindL2BDForNode(nodeName string, l2bdName string) *controller.L2BD {

	var n controller.Node
	var exists bool

	if n, exists = s.ramConfigCache.Nodes[nodeName]; !exists {
		return nil
	}
	for _, l2bd := range n.L2Bds {
		if l2bd.Name == l2bdName {
			return l2bd
		}
	}
	return nil
}

// FindVppL2BDForNode by name
func (s *Plugin) FindVppL2BDForNode(nodeName string, l2bdName string) (*controller.NodeState,
	*l2.BridgeDomains_BridgeDomain) {

	if l2bd := s.FindL2BDForNode(nodeName, l2bdName); l2bd == nil {
		return nil, nil
	}

	var vppKey *vppagentapi.KeyValueEntryType
	var exists bool

	// this might be wrong ... do we start rendering with a node l2bd with no if's

	// key := vppagentapi.L2BridgeDomainKey(nodeName, vppKeyL2BDName(nodeName, l2bdName))
	// if vppKey, exists = s.ramConfigCache.VppEntries[key]; !exists {
	// 	if vppKey, exists = s.configTransaction.afterEntriesMap[key]; !exists {
	// 		return nil, nil
	// 	}
	// }

	key := vppagentapi.L2BridgeDomainKey(nodeName, vppKeyL2BDName(nodeName, l2bdName))
	if vppKey, exists = s.configTransaction.afterEntriesMap[key]; !exists {
		return nil, nil
	}

	return s.ramConfigCache.NodeState[nodeName], vppKey.L2BD
}

func isLabelInCustomLabels(customLabels []string, label string) bool {
	for _, customLabel := range customLabels {
		if customLabel == label {
			return true
		}
	}
	return false
}

// NodeRenderVxlanStaticRoutes renders static routes for the vxlan
func (s *Plugin) NodeRenderVxlanStaticRoutes(fromNode, toNode,
	fromVxlanAddress, toVxlanAddress,
	OutgoingInterfaceLabel string) []*controller.RenderedVppAgentEntry {

	var renderedEntries []*controller.RenderedVppAgentEntry

	// depending on the number of ethernet/label:vxlan interfaces on the source node and
	// the number of ethernet/label:vxlan inerfaces on the dest node, a set of static
	// routes will be created

	// for now assume 1 address per node and soon there will ba a v4 and a v6 ?

	n1 := s.ramConfigCache.Nodes[fromNode]

	// make sure there is a loopback i/f entry for this vxlan endpoint
	vppKV := vppagentapi.ConstructLoopbackInterface(n1.Name,
		"IF_VXLAN_LOOPBACK_"+fromNode,
		[]string{fromVxlanAddress},
		"",
		s.ramConfigCache.SysParms.Mtu,
		controller.IfAdminStatusEnabled,
		s.ramConfigCache.SysParms.RxMode)
	renderedEntries = s.ConfigTransactionAddVppEntry(renderedEntries, vppKV)

	n2 := s.ramConfigCache.Nodes[toNode]

	for _, node1Iface := range n1.Interfaces {
		if node1Iface.IfType != controller.IfTypeEthernet ||
			!(isLabelInCustomLabels(node1Iface.CustomLabels, OutgoingInterfaceLabel) ||
				len(n1.Interfaces) == 1) { // if only one ethernet if, it does not need the label
			continue
		}
		for _, node2Iface := range n2.Interfaces {
			if node2Iface.IfType != controller.IfTypeEthernet ||
				!(isLabelInCustomLabels(node2Iface.CustomLabels, OutgoingInterfaceLabel) ||
					len(n2.Interfaces) == 1) { // if only one ethernet if, it does not need the label
				continue
			}

			l3sr := &controller.L3VRFRoute{
				VrfId:             0,
				Description:       fmt.Sprintf("L3VRF_VXLAN Node:%s to Node:%s", fromNode, toNode),
				DstIpAddr:         toVxlanAddress, // des node vxlan address
				NextHopAddr:       node2Iface.IpAddresses[0],
				OutgoingInterface: node1Iface.Name,
				Weight:            s.ramConfigCache.SysParms.DefaultStaticRouteWeight,
				Preference:        s.ramConfigCache.SysParms.DefaultStaticRoutePreference,
			}
			vppKV := vppagentapi.ConstructStaticRoute(n1.Name, l3sr)
			renderedEntries = s.ConfigTransactionAddVppEntry(renderedEntries, vppKV)
		}
	}
	return renderedEntries
}
