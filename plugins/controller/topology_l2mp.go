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

	"github.com/ligato/sfc-controller/plugins/controller/model"
	"github.com/ligato/sfc-controller/plugins/controller/vppagentapi"
	"github.com/ligato/vpp-agent/plugins/defaultplugins/common/model/l2"
)

// The L2MP topology is rendered in this module for a connection with a vnf-service

// RenderTopologyL2MP renders this L2MP connection
func (s *Plugin) RenderTopologyL2MP(vs *controller.VNFService,
	vnfs []*controller.VNF, conn *controller.Connection,
	connIndex uint32,
	vsState *controller.VNFServiceState) error {

	numIFs := len(conn.Interfaces)

	var v2n []controller.VNFToNodeMap
	vnfInterfaces := make([]*controller.Interface, 0)
	vnfTypes := make([]string, 0)

	allVnfsAssignedToNodes := true
	staticNodesInInterfacesSpecified := false

	var nodeMap = make(map[string]bool, 0) // determine the set of nodes

	log.Debugf("RenderTopologyL2MP: num interfaces: %d", numIFs)

	// let's see if all interfaces in the conn are associated with a node
	for _, connInterface := range conn.Interfaces {

		if connInterface.Vnf != "" {
			v, exists := s.ramConfigCache.VNFToNodeStateMap[connInterface.Vnf]
			if !exists || v.Node == "" {
				msg := fmt.Sprintf("connection segment: %s/%s, vnf not mapped to a node in vnf_to_node_map",
					connInterface.Vnf, connInterface.Interface)
				s.AppendStatusMsgToVnfService(msg, vsState)
				allVnfsAssignedToNodes = false
				continue
			}

			_, exists = s.ramConfigCache.Nodes[v.Node]
			if !exists {
				msg := fmt.Sprintf("connection segment: %s/%s, vnf references non existant host: %s",
					connInterface.Vnf, connInterface.Interface, v.Node)
				s.AppendStatusMsgToVnfService(msg, vsState)
				allVnfsAssignedToNodes = false
				continue
			}

			nodeMap[v.Node] = true // maintain a map of which nodes are in the conn set

			// based on the interfaces in the conn, order the interface info accordingly as the set of
			// interfaces in the vnf/interface stanza can be in a different order
			v2n = append(v2n, v)
			vnfInterface, vnfType := s.findVnfAndInterfaceInVnfList(connInterface.Vnf,
				connInterface.Interface, vnfs)
			vnfInterfaces = append(vnfInterfaces, vnfInterface)
			vnfTypes = append(vnfTypes, vnfType)
		}

		if connInterface.Node != "" {
			vnfInterface, vnfType := s.findInterfaceInNode(connInterface.Node,
				connInterface.Interface)
			var v controller.VNFToNodeMap
			v.Node = connInterface.Node
			v.Vnf = connInterface.Node
			v2n = append(v2n, v)
			vnfInterfaces = append(vnfInterfaces, vnfInterface)
			vnfTypes = append(vnfTypes, vnfType)
			staticNodesInInterfacesSpecified = true
		}
	}

	if !allVnfsAssignedToNodes {
		return fmt.Errorf("Not all vnfs in this connection are mapped to nodes")
	}

	log.Debugf("RenderTopologyL2MP: num unique nodes for this connection: %d", len(nodeMap))
	// log.Debugf("RenderTopologyL2MP: v2n=%v, vnfI=%v, conn=%v", v2n, vnfInterfaces, conn)

	// if a vnf service mesh is specified, see if it exists
	var vnfServiceMesh controller.VNFServiceMesh
	vnfServiceMeshExists := false
	if conn.VnfServiceMesh != "" {
		vnfServiceMesh, vnfServiceMeshExists = s.ramConfigCache.VNFServiceMeshes[conn.VnfServiceMesh]
		if !vnfServiceMeshExists {
			msg := fmt.Sprintf("vnf-service: %s, conn: %d, referencing a missing vnf service mesh",
				vs.Name,
				connIndex)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
	}

	// see if the vnfs are on the same node ...
	if len(nodeMap) == 1 {
		return s.renderToplogySegmentL2MPSameNode(vs, conn, connIndex, vnfInterfaces,
			&vnfServiceMesh, v2n, vnfTypes, vsState)
	} else {
		if staticNodesInInterfacesSpecified {
			msg := fmt.Sprintf("vnf-service: %s, all vnfs/nodes must match: %s",
				vs.Name,
				v2n[0].Node)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
	}

	// now setup the connection between nodes
	return s.renderToplogySegmentL2MPInterNode(vs, conn, connIndex, vnfInterfaces,
		&vnfServiceMesh, v2n, vnfTypes, nodeMap, vsState)
}

// renderToplogySegemtL2MPSameNode renders this L2MP connection set on same node
func (s *Plugin) renderToplogySegmentL2MPSameNode(vs *controller.VNFService,
	conn *controller.Connection,
	connIndex uint32,
	vnfInterfaces []*controller.Interface,
	vnfServiceMesh *controller.VNFServiceMesh,
	v2n []controller.VNFToNodeMap,
	vnfTypes []string,
	vsState *controller.VNFServiceState) error {

	// The interfaces should be created in the vnf and the vswitch then the vswitch
	// interfaces will be added to the bridge.

	var l2bdIFs = make(map[string][]*l2.BridgeDomains_BridgeDomain_Interfaces, 0)

	nodeName := v2n[0].Node

	for i := 0; i < len(conn.Interfaces); i++ {

		ifName, err := s.RenderToplogyInterfacePair(vs, nodeName, conn.Interfaces[i],
			vnfInterfaces[i], vnfTypes[i], vsState)
		if err != nil {
			return err
		}
		l2bdIF := &l2.BridgeDomains_BridgeDomain_Interfaces{
			Name: ifName,
			BridgedVirtualInterface: false,
		}
		l2bdIFs[nodeName] = append(l2bdIFs[nodeName], l2bdIF)
	}

	// all VNFs are on the same node so no vxlan inter-node mesh code required but
	// the VNFs might be connected to an external node/router via hub and spoke

	if vnfServiceMesh.ServiceMeshType == controller.VNFServiceMeshTypeHubAndSpoke &&
		vnfServiceMesh.ConnectionType == controller.VNFServiceMeshConnectionTypeVxlan {

			// construct a spoke set with this one one
			singleSpokeMap := make(map[string]bool)
			singleSpokeMap[nodeName] = true

			return s.renderToplogyL2MPVxlanHubAndSpoke(vs,
				conn,
				connIndex,
				vnfInterfaces,
				vnfServiceMesh,
				v2n,
				vnfTypes,
				singleSpokeMap,
				l2bdIFs,
				vsState)
	}

	// no external hub and spoke so simply render the nodes l2bd and local vnf interfaces
	return s.renderL2BD(vs, conn, connIndex, nodeName, l2bdIFs[nodeName], vsState)
}

func (s *Plugin) renderL2BD(vs *controller.VNFService,
	conn *controller.Connection, connIndex uint32,
	nodeName string,
	l2bdIFs []*l2.BridgeDomains_BridgeDomain_Interfaces,
	vsState *controller.VNFServiceState) error {

	// if using an existing node level bridge, we simply add the i/f's to the bridge
	if conn.UseNodeL2Bd != "" {

		var nodeState *controller.NodeState
		var nodeL2BD *l2.BridgeDomains_BridgeDomain

		// find the l2db for this node ...
		if nodeState, nodeL2BD = s.FindVppL2BDForNode(nodeName, conn.UseNodeL2Bd); nodeL2BD == nil {
			msg := fmt.Sprintf("vnf-service: %s, referencing a missing node/l2bd: %s/%s",
				vs.Name, nodeName, conn.UseNodeL2Bd)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
		vppKV := vppagentapi.AppendInterfacesToL2BD(nodeName, nodeL2BD, l2bdIFs)
		nodeState.RenderedVppAgentEntries =
			s.ConfigTransactionAddVppEntry(nodeState.RenderedVppAgentEntries, vppKV)

	} else {
		var bdParms *controller.BDParms
		if conn.L2Bd != nil {
			// need to create a bridge for this conn
			if conn.L2Bd.L2BdTemplate != "" {
				bdParms = s.FindL2BDTemplate(conn.L2Bd.L2BdTemplate)
			} else {
				bdParms = conn.L2Bd.BdParms
			}
		} else {
			bdParms = s.GetDefaultSystemBDParms()
		}
		vppKV := vppagentapi.ConstructL2BD(
			nodeName,
			fmt.Sprintf("L2BD_%s_CONN_%d", vs.Name, connIndex+1),
			l2bdIFs,
			bdParms)
		vsState.RenderedVppAgentEntries =
			s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	}
	return nil
}

// renderToplogySegmentLMPPInterNode renders this L2MP connection between nodes
func (s *Plugin) renderToplogySegmentL2MPInterNode(vs *controller.VNFService,
	conn *controller.Connection,
	connIndex uint32,
	vnfInterfaces []*controller.Interface,
	vnfServiceMesh *controller.VNFServiceMesh,
	v2n []controller.VNFToNodeMap,
	vnfTypes []string,
	nodeMap map[string]bool,
	vsState *controller.VNFServiceState) error {

	// The interfaces may be spread accross a set of nodes (nodeMap), each of these
	// interfaces should be created in the vnf and node's vswitch.  Then for each
	// node, these must be associated with a per node l2bd.  This might be an
	// existing node l2bd, or a bd that must be created.  The other matter is the
	// inter-node connectivity.  Example: if vxlan mesh is the chosen inter node
	// strategy, for each node in the nodeMap, a vxlan tunnel mesh must be created
	// using a free vni from the mesh's vniPool.

	var l2bdIFs = make(map[string][]*l2.BridgeDomains_BridgeDomain_Interfaces, 0)

	// create the vnf-interfaces from the vnf to the vswitch, note that depending on
	// the meshing strategy, I might have to create the interfaces with vrf_id's for
	// example, or ...
	for i := 0; i < len(conn.Interfaces); i++ {

		ifName, err := s.RenderToplogyInterfacePair(vs, v2n[i].Node, conn.Interfaces[i],
			vnfInterfaces[i], vnfTypes[i], vsState)
		if err != nil {
			return err
		}
		l2bdIF := &l2.BridgeDomains_BridgeDomain_Interfaces{
			Name: ifName,
			BridgedVirtualInterface: false,
			SplitHorizonGroup:       0,
		}
		l2bdIFs[v2n[i].Node] = append(l2bdIFs[v2n[i].Node], l2bdIF)
	}

	switch vnfServiceMesh.ConnectionType {
	case controller.VNFServiceMeshConnectionTypeVxlan:
		switch vnfServiceMesh.ServiceMeshType {
		case controller.VNFServiceMeshTypeMesh:
			return s.renderToplogyL2MPVxlanMesh(vs,
				conn,
				connIndex,
				vnfInterfaces,
				vnfServiceMesh,
				v2n,
				vnfTypes,
				nodeMap,
				l2bdIFs,
				vsState)
		case controller.VNFServiceMeshTypeHubAndSpoke:
			return s.renderToplogyL2MPVxlanHubAndSpoke(vs,
				conn,
				connIndex,
				vnfInterfaces,
				vnfServiceMesh,
				v2n,
				vnfTypes,
				nodeMap,
				l2bdIFs,
				vsState)
		}
	default:
		msg := fmt.Sprintf("vnf-service: %s, conn: %d, service mesh: %s type not implemented",
			vs.Name,
			connIndex,
			vnfServiceMesh.Name)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	return nil
}
