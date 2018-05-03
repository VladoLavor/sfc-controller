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
	staticNodesInInterfacesSpecified := false

	log.Debugf("RenderTopologyL2PP: num interfaces: %d", len(conn.Interfaces))

	numInterfacesInConn := len(conn.Interfaces)

	// let see if all interfaces in the conn are associated with a node
	for i, connInterface := range conn.Interfaces {

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

			v2n[i] = v
			vnfInterface, vnfType := s.findVnfAndInterfaceInVnfList(connInterface.Vnf,
				connInterface.Interface, vnfs)
			vnfInterfaces[i] = vnfInterface
			vnfTypes[i] = vnfType
		}

		if connInterface.Node != "" {
			vnfInterface, vnfType := s.findInterfaceInNode(connInterface.Node,
				connInterface.Interface)
			vnfInterfaces[i] = vnfInterface
			vnfTypes[i] = vnfType
			v2n[i].Node = connInterface.Node
			v2n[i].Vnf = connInterface.Node
			staticNodesInInterfacesSpecified = true
		}
	}

	if !allVnfsAssignedToNodes {
		msg := fmt.Sprintf("vnf-service: %s, Not all vnfs in this connection are mapped to nodes",
			vs.Name)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	if conn.NodeInterfaceLabels != nil {
	    if numInterfacesInConn != 1 || len(conn.NodeInterfaceLabels) != 1 {
			msg := fmt.Sprintf("vnf-service: %s, need 1 interface in conn and  1 nodeInterface label: incorrect config",
				vs.Name)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
		nodeInterfaces, nodeIfTypes := s.findInterfacesForThisLabelInNode(v2n[0].Node, conn.NodeInterfaceLabels)
		if len(nodeInterfaces) != 1 {
			msg := fmt.Sprintf("vnf-service: %s, nodeLabels %v: must match only 1 node interface: incorrect config",
				vs.Name, conn.NodeInterfaceLabels)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
		connInterface := &controller.Connection_Interface{
			Node: v2n[0].Node,
			Interface: nodeInterfaces[0].Name,
		}
		conn.Interfaces = append(conn.Interfaces, connInterface)
		vnfInterfaces[1] = nodeInterfaces[0]
		vnfTypes[1] = nodeIfTypes[0]
		v2n[1].Node = v2n[0].Node
		v2n[1].Vnf = v2n[0].Node
	}

	log.Debugf("RenderTopologyL2PP: v2n=%v, vnfI=%v, conn=%v", v2n, vnfInterfaces, conn)

	// see if the vnfs are on the same node ...
	if v2n[0].Node == v2n[1].Node {
		return s.renderToplogySegmentL2PPSameNode(vs, v2n[0].Node, conn, connIndex,
			vnfInterfaces, vnfTypes, vsState)
	} else if staticNodesInInterfacesSpecified {
		msg := fmt.Sprintf("vnf-service: %s, vnf/node %s/%s must be the same",
			vs.Name,
			v2n[0].Node,
			v2n[1].Node)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	// not on same node so ensure there is an VNFServiceMesh specified
	if conn.VnfServiceMesh == "" {
		msg := fmt.Sprintf("vnf-service: %s, %s/%s to %s/%s no node service mesh specified",
			vs.Name,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	// look up the vnf service mesh
	vnfServiceMesh, exists := s.ramConfigCache.VNFServiceMeshes[conn.VnfServiceMesh]
	if !exists {
		msg := fmt.Sprintf("vnf-service: %s, %s/%s to %s/%s referencing a missing vnf service mesh",
			vs.Name,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	// now setup the connection between nodes
	return s.renderToplogySegmentL2PPInterNode(vs, conn, connIndex, vnfInterfaces,
		&vnfServiceMesh, v2n, vnfTypes, vsState)
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
	vnfServiceMesh *controller.VNFServiceMesh,
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

	switch vnfServiceMesh.ConnectionType {
	case controller.VNFServiceMeshConnectionTypeVxlan:
		switch vnfServiceMesh.ServiceMeshType {
		case controller.VNFServiceMeshTypeMesh:
			return s.renderToplogyL2PPVxlanMesh(vs,
				conn,
				connIndex,
				vnfInterfaces,
				vnfServiceMesh,
				v2n,
				xconn,
				vsState)
		case controller.VNFServiceMeshTypeHubAndSpoke:
			msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s service mesh: %s type not supported for L2PP",
				vs.Name,
				connIndex,
				conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
				conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
				vnfServiceMesh.Name)
			s.AppendStatusMsgToVnfService(msg, vsState)
			return fmt.Errorf(msg)
		}
	default:
		msg := fmt.Sprintf("vnf-service: %s, conn: %d, %s/%s to %s/%s service mesh: %s type not implemented",
			vs.Name,
			connIndex,
			conn.Interfaces[0].Vnf, conn.Interfaces[0].Interface,
			conn.Interfaces[1].Vnf, conn.Interfaces[1].Interface,
			vnfServiceMesh.Name)
		s.AppendStatusMsgToVnfService(msg, vsState)
		return fmt.Errorf(msg)
	}

	return nil
}
