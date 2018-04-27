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

// The core plugin which drives the SFC Controller.  The core initializes the
// CNP dirver plugin based on command line args.  The database is initialized,
// and a resync is preformed based on what was already in the database.

package controller

import (
	"fmt"

	"github.com/ligato/sfc-controller/plugins/controller/model"
)

func (s *Plugin) findVnfAndInterfaceInVnfList(vnfName string, ifName string,
	vnfs []*controller.VNF) (*controller.Interface, string) {

	for _, vnf := range vnfs {
		for _, iFace := range vnf.Interfaces {
			if vnf.Name == vnfName && iFace.Name == ifName {
				return iFace, vnf.VnfType
			}
		}
	}
	return nil, ""
}

// vnfServiceValidateConnections validates all the fields
func (s *Plugin) vnfServiceValidateConnections(vsName string,
	vnfs []*controller.VNF,
	connections []*controller.Connection) error {

	// traverse the connections and for each vnf/interface, it must be in the
	// list of vnf's

	for _, conn := range connections {

		switch conn.ConnType {
		case controller.ConnTypeL2PP:
			if len(conn.Interfaces) != 2 {
				return fmt.Errorf("vnf-service: %s conn: p2p must have 2 interfaces only",
					vsName)
			}
		case controller.ConnTypeL2MP:
			if len(conn.Interfaces) == 0 {
				return fmt.Errorf("vnf-service: %s conn: must have at least one interface only",
					vsName)
			}
			if conn.UseNodeL2Bd != "" && conn.L2Bd != nil {
				return fmt.Errorf("vnf-service: %s conn: cannot refer to a node bd AND provide l2bd parameters",
					vsName)
			}
			if conn.L2Bd != nil {
				if conn.L2Bd.L2BdTemplate != "" && conn.L2Bd.BdParms != nil {
					return fmt.Errorf("vnf-service: %s conn: l2bd: %s  cannot refer to temmplate and provide l2bd parameters",
						vsName, conn.L2Bd.Name)
				}
				if conn.L2Bd.L2BdTemplate != "" {
					if l2bdt := s.FindL2BDTemplate(conn.L2Bd.L2BdTemplate); l2bdt == nil {
						return fmt.Errorf("vnf-service: %s, conn: l2bd: %s  has invalid reference to non-existant l2bd template '%s'",
							vsName, conn.L2Bd.Name, conn.L2Bd.L2BdTemplate)
					}
				} else if conn.L2Bd.BdParms == nil {
					return fmt.Errorf("vnf-service: %s, conn: l2bd: %s has no db parms nor refer to a template",
						vsName, conn.L2Bd.Name)
				}
			}
		default:
			return fmt.Errorf("vnf-service: %s, connection has invalid conn type '%s'",
				vsName, conn.ConnType)
		}

		for _, connInterface := range conn.Interfaces {

			if connInterface.Vnf == "" && connInterface.Node == "" {
				return fmt.Errorf("vnf-service: %s conn: vnf/node: %s/%s one must be set in vnf interfaces",
					vsName, connInterface.Vnf, connInterface.Node)
			}
			if connInterface.Vnf != "" && connInterface.Node != "" {
				return fmt.Errorf("vnf-service: %s conn: vnf/node: %s/%s cannot both be set in vnf interfaces",
					vsName, connInterface.Vnf, connInterface.Node)
			}
			if connInterface.Vnf != "" {
				if iface, _ := s.findVnfAndInterfaceInVnfList(connInterface.Vnf,
					connInterface.Interface, vnfs); iface == nil {
					return fmt.Errorf("vnf-service: %s conn: vnf/port: %s/%s not found in vnf interfaces",
						vsName, connInterface.Vnf, connInterface.Interface)
				}
			}
			if connInterface.Node != "" {
				if iFace, _ := s.findInterfaceInNode(connInterface.Node,
					connInterface.Interface); iFace == nil {
					return fmt.Errorf("vnf-service: %s conn: node/port: %s/%s not found in node's interfaces",
						vsName, connInterface.Node, connInterface.Interface)
				}
			}
		}
	}

	return nil
}

// vnfServiceValidateVnfs validates all the fields
func (s *Plugin) vnfServiceValidateVnfs(vsName string,
	vnfs []*controller.VNF) error {

	// traverse the vnfs and for each vnf/interface, it must be in the
	// list of vnf's

	for _, vnf := range vnfs {
		if err := s.ValidateVnfs(vsName, vnf); err != nil {
			return err
		}
	}

	return nil
}

// VNFServiceValidate validates all the fields
func (s *Plugin) VNFServiceValidate(vs *controller.VNFService) error {

	if vs.Vnfs == nil || len(vs.Vnfs) == 0 {
		return fmt.Errorf("vnf-service: %s vnf's are missing from this service",
			vs.Name)
	}

	if err := s.vnfServiceValidateVnfs(vs.Name, vs.Vnfs); err != nil {
		return err
	}

	if vs.Connections != nil {
		if err := s.vnfServiceValidateConnections(vs.Name, vs.Vnfs, vs.Connections); err != nil {
			return err
		}
	}

	return nil
}

// VNFServiceCreate add to ram cache and run topology side effects
func (s *Plugin) VNFServiceCreate(vs *controller.VNFService, render bool) error {

	if err := s.VNFServiceValidate(vs); err != nil {
		return err
	}

	if render {
		if err := s.RenderVNFService(vs); err != nil {
			return err
		}
	}

	s.ramConfigCache.VNFServices[vs.Name] = *vs

	if err := s.VNFServiceWriteToDatastore(vs); err != nil {
		return err
	}

	// inform ipam pool that a new vs might need a vnf service scope pool allocated
	s.IPAMPoolEntityCreate(vs.Name)

	log.Debugf("VNFServiceCreate: %v", s.ramConfigCache.VNFServices[vs.Name])

	return nil
}

// VNFServicesRender adapts to resource changes
func (s *Plugin) VNFServicesRender() error {

	// traverse each service, and render segments if possible
	for _, vs := range s.ramConfigCache.VNFServices {
		log.Debugf("VNFServicesRerender: vs: ", vs)
		if err := s.RenderVNFService(&vs); err != nil {
			return err
		}
	}
	return nil
}

// RenderVNFService traverses vnfs, connections and renders the service
func (s *Plugin) RenderVNFService(vs *controller.VNFService) error {

	if vsState, exists := s.ramConfigCache.VNFServicesState[vs.Name]; exists {
		// add the current rendered etcd keys to the before config transaction
		s.CopyRenderedVppAgentEntriesToBeforeConfigTransaction(vsState.RenderedVppAgentEntries)
	}

	// clear the ram cache entry, add state to it as we render, then add it to
	// the datastore?
	delete(s.ramConfigCache.VNFServicesState, vs.Name)

	vsState := &controller.VNFServiceState{
		Name: vs.Name,
	}

	// as rendering occurs, the new entries will be added the after resync config transaction

	for i, conn := range vs.Connections {
		log.Debugf("RenderVNFService: vnf-service/conn: ", vs.Name, conn)

		if err := s.RenderConnectionSegments(vs, vs.Vnfs, conn, uint32(i), vsState); err != nil {
			s.ConfigCleanupErrorOcurredDuringRendering()
			vsState.RenderedVppAgentEntries = nil
			log.Errorf("RenderVNFService: fail in vnf-service: %s, state=%v", vs.Name, vsState)
		}
	}

	if len(vsState.Msg) == 0 {
		s.AppendStatusMsgToVnfService("OK", vsState)
		vsState.OperStatus = controller.VNFServiceOperStatusUp
	} else {
		vsState.OperStatus = controller.VNFServiceOperStatusDown
	}

	s.ramConfigCache.VNFServicesState[vs.Name] = vsState

	if err := s.VNFServiceStatusWriteToDatastore(vsState); err != nil {
		return err
	}

	log.Debugf("RenderVNFService: vnf-service:%s, status:%v", vs.Name, vsState)

	return nil
}

// RenderConnectionSegments trraverses the connections and renders them
func (s *Plugin) RenderConnectionSegments(vs *controller.VNFService,
	vnfs []*controller.VNF,
	conn *controller.Connection,
	i uint32,
	vsState *controller.VNFServiceState) error {

	log.Debugf("RenderConnectionSegments: connType: %s", conn.ConnType)

	switch conn.ConnType {
	case controller.ConnTypeL2PP:
		if err := s.RenderTopologyL2PP(vs, vnfs, conn, i, vsState); err != nil {
			return err
		}
	case controller.ConnTypeL2MP:
		if err := s.RenderTopologyL2MP(vs, vnfs, conn, i, vsState); err != nil {
			return err
		}
	}

	return nil
}

// AppendStatusMsgToVnfService appends a msg for a vnf-service for state information
func (s *Plugin) AppendStatusMsgToVnfService(msg string, vsState *controller.VNFServiceState) {
	vsState.Msg = append(vsState.Msg, msg)
}
