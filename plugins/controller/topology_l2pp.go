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
)

// The L2PP topology is rendered in this module for a connection with a vnf-service

// RenderTopologyL2PP renders this L2PP connection
func (s *Plugin) RenderTopologyL2PP(vs *controller.VNFService,
	vnfs []*controller.VNF, conn *controller.Connection, connIndex uint32,
	vsState *controller.VNFServiceState) error {

	var v2n [2]controller.VNFToNodeMap
	vnfInterfaces := make([]*controller.Interface, 2)
	vnfTypes := make([]string, 2)

	allVnfsAssignedToNodes := true

	log.Debugf("RenderTopologyL2PP: num interfaces: %d", len(conn.Interfaces))

	// let see if all interfaces in the conn are associated with a node
	for i, connInterface := range conn.Interfaces {

		v, exists := s.ramConfigCache.VNFToNodeMap[connInterface.Vnf]
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

		v2n[i] = v
		vnfInterface, vnfType := s.findVnfAndInterfaceInVnfList(connInterface.Vnf,
			connInterface.Interface, vnfs)
		vnfInterfaces[i] = vnfInterface
		vnfTypes[i] = vnfType
	}

	if !allVnfsAssignedToNodes {
		return fmt.Errorf("Not all vnfs in this connection are mapped to nodes")
	}

	log.Debugf("RenderTopologyL2PP: v2n=%v, vnfI=%v, conn=%v", v2n, vnfInterfaces, conn)

	// see if the vnfs are on the same node ...
	if v2n[0].Node == v2n[1].Node {
		return s.renderToplogySegmentL2PPSameNode(vs, v2n[0].Node, conn, connIndex,
			vnfInterfaces, vnfTypes, vsState)
	}

	// not on same node so ensure there is an nodeOverlay sepcified
	if conn.NodeOverlay == "" {
		msg := fmt.Sprintf("vnf-service: %s, %s/%s to %s/%s no node overlay specified",
			vs.Name,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	// look up the node overlay
	nodeOverlay, exists := s.ramConfigCache.NodeOverlays[conn.NodeOverlay]
	if !exists {
		msg := fmt.Sprintf("vnf-service: %s, %s/%s to %s/%s referencing a missing node overlay",
			vs.Name,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	// now setup the connection between nodes
	return s.renderToplogySegmentL2PPInterNode(vs, conn, connIndex, vnfInterfaces,
		&nodeOverlay, v2n, vnfTypes, vsState)
}

// renderToplogySegemtL2PPSameNode renders this L2PP connection on same node
func (s *Plugin) renderToplogySegmentL2PPSameNode(vs *controller.VNFService,
	vppAgent string,
	conn *controller.Connection,
	connIndex uint32,
	vnfInterfaces []*controller.Interface,
	vnfTypes []string,
	vsState *controller.VNFServiceState) error {

	// if both interfaces are memIf's, we can do a direct inter-vnf memif
	// otherwise, each interface drops into the vswitch and an l2xc is used
	// to connect the interfaces inside the vswitch
	// both interfaces can override direct by specifying "vswitch" as its
	// inter vnf connection type

	memifConnType := controller.IfMemifInterVnfConnTypeDirect // assume direct
	for i := 0; i < 2; i++ {
		if vnfInterfaces[i].MemifParms != nil {
			if vnfInterfaces[i].MemifParms.InterVnfConn != "" &&
				vnfInterfaces[i].MemifParms.InterVnfConn != controller.IfMemifInterVnfConnTypeDirect {
				memifConnType = vnfInterfaces[i].MemifParms.InterVnfConn
			}
		}
	}

	if vnfInterfaces[0].IfType == vnfInterfaces[1].IfType &&
		vnfInterfaces[0].IfType == controller.IfTypeMemif &&
		memifConnType == controller.IfMemifInterVnfConnTypeDirect {

		err := s.RenderToplogyDirectInterVnfMemifPair(vs, conn, vnfInterfaces, controller.IfTypeMemif, vsState)
		if err != nil {
			return err
		}

	} else {

		var xconn [2]string
		// render the if's, and then l2xc them
		for i := 0; i < 2; i++ {

			ifName, err := s.RenderToplogyInterfacePair(vs, vppAgent, conn.Interfaces[i],
				vnfInterfaces[i], vnfTypes[i], vsState)
			if err != nil {
				return err
			}
			xconn[i] = ifName
		}

		for i := 0; i < 2; i++ {
			// create xconns between vswitch side of the container interfaces and the vxlan ifs
			vppKVs := vppagentapi.ConstructXConnect(vppAgent, xconn[i], xconn[^i&1])
			vsState.RenderedVppAgentEntries =
				s.ConfigTransactionAddVppEntries(vsState.RenderedVppAgentEntries, vppKVs)

		}
	}

	return nil
}

// renderToplogySegmentL2PPInterNode renders this L2PP connection between nodes
func (s *Plugin) renderToplogySegmentL2PPInterNode(vs *controller.VNFService,
	conn *controller.Connection,
	connIndex uint32,
	vnfInterfaces []*controller.Interface,
	nodeOverlay *controller.NodeOverlay,
	v2n [2]controller.VNFToNodeMap,
	vnfTypes []string,
	vsState *controller.VNFServiceState) error {

	var xconn [2][2]string // [0][i] for vnf interfaces [1][i] for vxlan

	// create the interfaces in the containers and vswitch on each node
	for i := 0; i < 2; i++ {

		ifName, err := s.RenderToplogyInterfacePair(vs, v2n[i].Node, conn.Interfaces[i],
			vnfInterfaces[i], vnfTypes[i], vsState)
		if err != nil {
			return err
		}
		xconn[0][i] = ifName
	}

	// hack for now ... only inter-node tunnel supported
	if nodeOverlay.NodeOverlayType != controller.NodeOverlayTypeMesh &&
		nodeOverlay.ConnectionType != controller.NodeOverlayConnectionTypeVxlan {
		msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s overlay: %s type not implemented",
			vs.Name,
			connIndex,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
			nodeOverlay.Name)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	// create the vxlan endpoints
	vniAllocator, exists := s.ramConfigCache.NodeOverlayVniAllocators[nodeOverlay.Name]
	if !exists {
		msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s overlay: %s out of vni's",
			vs.Name,
			connIndex,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
			nodeOverlay.Name)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}
	vni, err := vniAllocator.AllocateVni()
	if err != nil {
		msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s overlay: %s out of vni's",
			vs.Name,
			connIndex,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
			nodeOverlay.Name)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}
	for i := 0; i < 2; i++ {

		from := i
		to := ^i&1

		ifName := fmt.Sprintf("IF_VXLAN_L2PP_FROM_%s_%s_%s_TO_%s_%s_%s_VSRVC_%s_CONN_%d_VNI_%d",
			v2n[from].Node, conn.Interfaces[from].Vnf, conn.Interfaces[from].Interface,
			v2n[to].Node, conn.Interfaces[to].Vnf, conn.Interfaces[to].Interface,
			vs.Name, connIndex, vni)

		xconn[1][i] = ifName

		vxlanIPFromAddress, err := s.NodeOverlayAllocateVxlanAddress(
			nodeOverlay.VxlanMeshParms.LoopbackIpamPoolName, v2n[i].Node)
		if err != nil {
			msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s overlay: %s, %s",
				vs.Name,
				connIndex,
				conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
				conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
				nodeOverlay.Name, err)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
		vxlanIPToAddress, err := s.NodeOverlayAllocateVxlanAddress(
			nodeOverlay.VxlanMeshParms.LoopbackIpamPoolName, v2n[^i&1].Node)
		if err != nil {
			msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s overlay: %s %s",
				vs.Name,
				connIndex,
				conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
				conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
				nodeOverlay.Name, err)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}

		vppKV := vppagentapi.ConstructVxlanInterface(
			v2n[i].Node,
			ifName,
			vni,
			vxlanIPFromAddress,
			vxlanIPToAddress)
		vsState.RenderedVppAgentEntries =
			s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

		renderedEntries := s.NodeRenderVxlanStaticRoutes(v2n[i].Node, v2n[^i&1].Node,
			vxlanIPFromAddress, vxlanIPToAddress,
			nodeOverlay.VxlanMeshParms.OutgoingInterfaceLabel)

		vsState.RenderedVppAgentEntries = append(vsState.RenderedVppAgentEntries,
			renderedEntries...)
	}

	// create xconns between vswitch side of the container interfaces and the vxlan ifs
	for i := 0; i < 2; i++ {
		vppKVs := vppagentapi.ConstructXConnect(v2n[i].Node, xconn[0][i], xconn[1][i])
		vsState.RenderedVppAgentEntries =
			s.ConfigTransactionAddVppEntries(vsState.RenderedVppAgentEntries, vppKVs)
	}

	return nil
}
