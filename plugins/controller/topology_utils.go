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

// RenderToplogyInterfacePair renders this interface pair on the vnf and vswitch
func (s *Plugin) RenderToplogyInterfacePair(vs *controller.VNFService,
	vppAgent string,
	connInterface *controller.Connection_Interface,
	vnfInterface *controller.Interface,
	vnfType string,
	vsState *controller.VNFServiceState) (string, error) {

	// The interface should be created in the vnf and the vswitch then the vswitch
	// interfaces will be added to the bridge or used in a l2 xconn

	switch vnfInterface.IfType {
	case controller.IfTypeMemif:
		return s.RenderToplogyMemifPair(vs, vppAgent, connInterface, vnfInterface, vnfType, vsState)
	case controller.IfTypeVeth:
		return s.RenderToplogyVethAfpPair(vs, vppAgent, connInterface, vnfInterface, vnfType, vsState)
	case controller.IfTypeTap:
		return s.RenderToplogyTapPair(vs, vppAgent, connInterface, vnfInterface, vnfType, vsState)
	case controller.IfTypeEthernet:
		return vnfInterface.Name, nil
	}

	return "", nil
}

// RenderToplogyMemifPair renders this vnf/vswitch interface pair
func (s *Plugin) RenderToplogyMemifPair(vs *controller.VNFService,
	vppAgent string,
	connInterface *controller.Connection_Interface,
	vnfInterface *controller.Interface,
	vnfType string,
	vsState *controller.VNFServiceState) (string, error) {

	var ifName string

	ifState, err := s.initInterfaceState(vs, vppAgent, connInterface.Vnf,
		vnfInterface, vsState)
	if err != nil {
		return "", err
	}
	if ifState.MemifID == 0 {
		ifState.MemifID = s.ramConfigCache.MemifIDAllocator.Allocate()
	}
	s.persistInterfaceState(ifState, connInterface.Vnf,
		vnfInterface.Name)

	vppKV := vppagentapi.ConstructMemInterface(
		connInterface.Vnf,
		vnfInterface.Name,
		ifState.IpAddresses,
		ifState.MacAddress,
		s.ResolveMtu(vnfInterface.Mtu),
		vnfInterface.AdminStatus,
		s.ResolveRxMode(vnfInterface.RxMode),
		ifState.MemifID,
		false,
		vnfInterface.MemifParms,
		vppAgent)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	log.Debugf("RenderToplogyMemifPair: ifName: %s, %v", ifName, vppKV)

	ifName = "IF_MEMIF_VSWITCH_" + connInterface.Vnf + "_" + connInterface.Interface
	vppKV = vppagentapi.ConstructMemInterface(
		vppAgent,
		ifName,
		[]string{},
		"",
		s.ResolveMtu(vnfInterface.Mtu),
		vnfInterface.AdminStatus,
		s.ResolveRxMode(vnfInterface.RxMode),
		ifState.MemifID,
		true,
		vnfInterface.MemifParms,
		vppAgent)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	log.Debugf("RenderToplogyMemifPair: ifName: %s, %v", ifName, vppKV)

	return ifName, nil
}

func ipAddressArraysEqual(a1 []string, a2 []string) bool {
	if len(a1) != len(a2) {
		return false
	}
	foundCount := 0
	for _, e1 := range a1 {
		for _, e2 := range a2 {
			if e1 == e2 {
				foundCount++
				break
			}
		}
	}
	if foundCount != len(a1) {
		return false
	}

	return true
}

// RenderToplogyDirectInterVnfMemifPair renders this vnf/vswitch interface pair
func (s *Plugin) RenderToplogyDirectInterVnfMemifPair(vs *controller.VNFService,
	conn *controller.Connection,
	vnfInterfaces []*controller.Interface,
	vnfType string,
	vsState *controller.VNFServiceState) error {

	if0State, err := s.initInterfaceState(vs, conn.Interfaces[0].Vnf,
		conn.Interfaces[0].Vnf, vnfInterfaces[0], vsState)
	if err != nil {
		return err
	}
	if if0State.MemifID == 0 {
		if0State.MemifID = s.ramConfigCache.MemifIDAllocator.Allocate()
	}
	s.persistInterfaceState(if0State, conn.Interfaces[0].Vnf,
		vnfInterfaces[0].Name)

	vppKV := vppagentapi.ConstructMemInterface(
		conn.Interfaces[0].Vnf,
		vnfInterfaces[0].Name,
		if0State.IpAddresses,
		if0State.MacAddress,
		s.ResolveMtu(vnfInterfaces[0].Mtu),
		vnfInterfaces[0].AdminStatus,
		s.ResolveRxMode(vnfInterfaces[0].RxMode),
		if0State.MemifID,
		false,
		vnfInterfaces[0].MemifParms,
		conn.Interfaces[1].Vnf)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	log.Debugf("RenderToplogyDirectInterVnfMemifPair: ifName0: %s/%s, %v",
		conn.Interfaces[0].Vnf, vnfInterfaces[0].Name, vppKV)

	if1State, err := s.initInterfaceState(vs, conn.Interfaces[1].Vnf,
		conn.Interfaces[1].Vnf, vnfInterfaces[1], vsState)
	if err != nil {
		return err
	}
	if1State.MemifID = if0State.MemifID
	s.persistInterfaceState(if1State, conn.Interfaces[0].Vnf,
		vnfInterfaces[0].Name)

	vppKV = vppagentapi.ConstructMemInterface(
		conn.Interfaces[1].Vnf,
		vnfInterfaces[1].Name,
		if1State.IpAddresses,
		if1State.MacAddress,
		s.ResolveMtu(vnfInterfaces[1].Mtu),
		vnfInterfaces[1].AdminStatus,
		s.ResolveRxMode(vnfInterfaces[1].RxMode),
		if1State.MemifID,
		true,
		vnfInterfaces[1].MemifParms,
		conn.Interfaces[1].Vnf)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	log.Debugf("RenderToplogyDirectInterVnfMemifPair: ifName1: %s/%s, %v",
		conn.Interfaces[1].Vnf, vnfInterfaces[1].Name, vppKV)

	return nil
}

// RenderToplogyTapPair renders this vnf/vswitch tap interface pair
func (s *Plugin) RenderToplogyTapPair(vs *controller.VNFService,
	vppAgent string,
	connInterface *controller.Connection_Interface,
	vnfInterface *controller.Interface,
	vnfType string,
	vsState *controller.VNFServiceState) (string, error) {

	return "", fmt.Errorf("tap not supported")
}

// RenderToplogyVethAfpPair renders this vnf/vswitch veth/afp interface pair
func (s *Plugin) RenderToplogyVethAfpPair(vs *controller.VNFService,
	vppAgent string,
	connInterface *controller.Connection_Interface,
	vnfInterface *controller.Interface,
	vnfType string,
	vsState *controller.VNFServiceState) (string, error) {

	var ifName string

	ifState, err := s.initInterfaceState(vs, vppAgent, connInterface.Vnf,
		vnfInterface, vsState)
	if err != nil {
		return "", err
	}
	s.persistInterfaceState(ifState, connInterface.Vnf,
		vnfInterface.Name)

	// Create a VETH i/f for the vnf container, the ETH will get created
	// by the vpp-agent in a more privileged vswitch.
	// Note: In Linux kernel the length of an interface name is limited by
	// the constant IFNAMSIZ. In most distributions this is 16 characters
	// including the terminating NULL character. The hostname uses chars
	// from the container for a total of 15 chars.

	veth1Name := "IF_VETH_VNF_" + connInterface.Vnf + "_" + connInterface.Interface
	veth2Name := "IF_VETH_VSWITCH_" + connInterface.Vnf + "_" + connInterface.Interface
	host1Name := connInterface.Interface
	baseHostName := constructBaseHostName(connInterface.Vnf, connInterface.Interface)
	host2Name := baseHostName

	vethIPAddresses := ifState.IpAddresses
	if vnfType == controller.VNFTypeVPPContainer {
		vethIPAddresses = []string{}
	}
	// Configure the VETH interface for the VNF end
	vppKV := vppagentapi.ConstructVEthInterface(vppAgent,
		veth1Name,
		vethIPAddresses,
		ifState.MacAddress,
		s.ResolveMtu(vnfInterface.Mtu),
		vnfInterface.AdminStatus,
		host1Name,
		veth2Name,
		connInterface.Vnf)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	// Configure the VETH interface for the VSWITCH end
	vppKV = vppagentapi.ConstructVEthInterface(vppAgent,
		veth2Name,
		[]string{},
		"",
		s.ResolveMtu(vnfInterface.Mtu),
		vnfInterface.AdminStatus,
		host2Name,
		veth1Name,
		vppAgent)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	// Configure the AFP interface for the VNF end
	if vnfType == controller.VNFTypeVPPContainer {
		vppKV = vppagentapi.ConstructAFPacketInterface(connInterface.Vnf,
			vnfInterface.Name,
			ifState.IpAddresses,
			ifState.MacAddress,
			s.ResolveMtu(vnfInterface.Mtu),
			vnfInterface.AdminStatus,
			s.ResolveRxMode(vnfInterface.RxMode),
			host1Name)
		vsState.RenderedVppAgentEntries =
			s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)
	}
	// Configure the AFP interface for the VSWITCH end
	ifName = "IF_AFPIF_VSWITCH_" + connInterface.Vnf + "_" + connInterface.Interface
	vppKV = vppagentapi.ConstructAFPacketInterface(vppAgent,
		ifName,
		[]string{},
		"",
		s.ResolveMtu(vnfInterface.Mtu),
		vnfInterface.AdminStatus,
		s.ResolveRxMode(vnfInterface.RxMode),
		host2Name)
	vsState.RenderedVppAgentEntries =
		s.ConfigTransactionAddVppEntry(vsState.RenderedVppAgentEntries, vppKV)

	return ifName, nil
}

func (s *Plugin) initInterfaceState(vs *controller.VNFService,
	vppAgent string,
	vnf string,
	vnfInterface *controller.Interface,
	vsState *controller.VNFServiceState) (*controller.InterfaceState, error) {

	ifState, exists := s.ramConfigCache.InterfaceStates[vnf+"/"+vnfInterface.Name]
	if !exists {
		ifState = controller.InterfaceState{
			Vnf:       vnf,
			Interface: vnfInterface.Name,
			Node:      vppAgent,
		}
	}

	if vnfInterface.MacAddress == "" {
		if ifState.MacAddress == "" {
			ifState.MacAddress = s.ramConfigCache.MacAddrAllocator.Allocate()
		}
	} else {
		if ifState.MacAddress != vnfInterface.MacAddress {
			ifState.MacAddress = vnfInterface.MacAddress
		}
	}
	if len(vnfInterface.IpAddresses) == 0 {
		if len(ifState.IpAddresses) == 0 {
			if vnfInterface.IpamPoolName != "" {
				ipAddress, err := s.IPAMPoolAllocateAddress(vnfInterface.IpamPoolName,
					vppAgent, vsState.Name)
				if err != nil {
					return nil, err
				}
				ifState.IpAddresses = []string{ipAddress}
			}
		}
	} else {
		if !ipAddressArraysEqual(ifState.IpAddresses, vnfInterface.IpAddresses) {
			// ideally we would free up the addresses of ifState and
			ifState.IpAddresses = vnfInterface.IpAddresses
		}
	}

	return &ifState, nil
}

func (s *Plugin) persistInterfaceState(ifState *controller.InterfaceState,
	vnf string, interfaceName string) {

	s.InterfaceStateWriteToDatastore(ifState)
	s.ramConfigCache.InterfaceStates[vnf+"/"+interfaceName] = *ifState
}

func stringFirstNLastM(n int, m int, str string) string {
	if len(str) <= n+m {
		return str
	}
	outStr := ""
	for i := 0; i < n; i++ {
		outStr += fmt.Sprintf("%c", str[i])
	}
	for i := 0; i < m; i++ {
		outStr += fmt.Sprintf("%c", str[len(str)-m+i])
	}
	return outStr
}

func constructBaseHostName(container string, port string) string {

	// Use at most 8 chrs from cntr name, and 7 from port
	// If cntr is less than 7 then can use more for port and visa versa.  Also, when cntr and port name
	// is more than 7 chars, use first few chars and last few chars from name ... brain dead scheme?
	// will it be readable?

	cb := 4 // 4 from beginning of container string
	ce := 4 // 4 from end of container string
	pb := 3 // 3 from beginning of port string
	pe := 4 // 4 from end of port string

	if len(container) < 8 {
		// increase char budget for port if container is less than max budget of 8
		switch len(container) {
		case 7:
			pb++
		case 6:
			pb++
			pe++
		case 5:
			pb += 2
			pe++
		case 4:
			pb += 2
			pe += 2
		case 3:
			pb += 3
			pe += 2
		case 2:
			pb += 3
			pe += 3
		case 1:
			pb += 4
			pe += 3
		}
	}

	if len(port) < 7 {
		// increase char budget for container if port is less than max budget of 7
		switch len(port) {
		case 6:
			cb++
		case 5:
			cb++
			ce++
		case 4:
			cb += 2
			ce++
		case 3:
			cb += 2
			ce += 2
		case 2:
			cb += 3
			ce += 2
		case 1:
			cb += 3
			ce += 3
		}
	}

	return stringFirstNLastM(cb, ce, container) + stringFirstNLastM(pb, pe, port)
}
