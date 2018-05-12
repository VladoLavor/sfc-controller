// Copyright (c) 2018 Cisco and/or its affiliates.
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

//go:generate protoc --proto_path=model --gogo_out=model model/controller.proto

package controller

import (
	"os"

	"github.com/ligato/cn-infra/core"
	"github.com/ligato/cn-infra/db/keyval"
	"github.com/ligato/cn-infra/db/keyval/etcdv3"
	"github.com/ligato/cn-infra/flavors/local"
	"github.com/ligato/cn-infra/health/statuscheck"
	"github.com/ligato/cn-infra/logging"
	"github.com/ligato/cn-infra/logging/logrus"
	"github.com/ligato/cn-infra/rpc/rest"
	"github.com/ligato/sfc-controller/plugins/controller/database"
	"github.com/ligato/sfc-controller/plugins/controller/idapi"
	"github.com/ligato/sfc-controller/plugins/controller/idapi/ipam"
	"github.com/ligato/sfc-controller/plugins/controller/model"
	"github.com/ligato/sfc-controller/plugins/controller/vppagent"
	"github.com/namsral/flag"
	"github.com/unrolled/render"
	"net/http"
)

// PluginID is plugin identifier (must be unique throughout the system)
const PluginID core.PluginName = "SfcController"

var (
	sfcConfigFile     string // cli flag - see RegisterFlags
	cleanSfcDatastore bool   // cli flag - see RegisterFlags
	contivKSREnabled  bool   // cli flag - see RegisterFlags
	BypassModelTypeHttpHandlers  bool   // cli flag - see RegisterFlags
	log               = logrus.DefaultLogger()
	ctlrPlugin        *Plugin
)

// RegisterFlags add command line flags.
func RegisterFlags() {
	flag.StringVar(&sfcConfigFile, "sfc-config", "",
		"Name of a sfc config (yaml) file to load at startup")
	flag.BoolVar(&cleanSfcDatastore, "clean", false,
		"Clean the SFC datastore entries")
	flag.BoolVar(&contivKSREnabled, "contiv-ksr", false,
		"Interact with contiv ksr to learn k8s config/state")
	flag.BoolVar(&BypassModelTypeHttpHandlers, "bypass-rest-for-model-objects", false,
		"Disable HTTP handling for controller objects")
}

// LogFlags dumps the command line flags
func LogFlags() {
	log.Debugf("LogFlags:")
	log.Debugf("\tsfcConfigFile:'%s'", sfcConfigFile)
	log.Debugf("\tclean:'%v'", cleanSfcDatastore)
	log.Debugf("\tcontiv ksr:'%v'", contivKSREnabled)
	log.Debugf("\tmodel REST disabled:'%v'", BypassModelTypeHttpHandlers)
}

func init() {
	// Logger must be initialized for each s individually.
	//log.SetLevel(logging.DebugLevel)
	log.SetLevel(logging.InfoLevel)

	RegisterFlags()
}

// CacheType is ram cache of controller entities
type CacheType struct {
	// state
	InterfaceStates     map[string]*controller.InterfaceStatus
	VppEntries          map[string]*vppagent.KVType
	MacAddrAllocator    *idapi.MacAddrAllocatorType
	MemifIDAllocator    *idapi.MemifAllocatorType
	IPAMPoolAllocators  map[string]*ipam.PoolAllocatorType
	NetworkPodToNodeMap map[string]*NetworkPodToNodeMap
}

// Plugin contains the controllers information
type Plugin struct {
	Etcd    *etcdv3.Plugin
	HTTPmux *rest.Plugin
	*local.FlavorLocal
	NetworkNodeMgr              NetworkNodeMgr
	IpamPoolMgr                 IPAMPoolMgr
	SysParametersMgr            SystemParametersMgr
	NetworkServiceMgr           NetworkServiceMgr
	NetworkNodeOverlayMgr       NetworkNodeOverlayMgr
	NetworkPodNodeMapMgr        NetworkPodToNodeMapMgr
	ramConfigCache              CacheType
	db                          keyval.ProtoBroker
}

// Init the controller, read the db, reconcile/resync, render config to etcd
func (s *Plugin) Init() error {

	ctlrPlugin = s

	log.Infof("Init: %s enter ...", PluginID)
	defer log.Infof("Init: %s exit ", PluginID)

	// Flag variables registered in init() are ready to use in InitPlugin()
	LogFlags()

	// Register providing status reports (push mode)
	s.StatusCheck.Register(PluginID, nil)
	s.StatusCheck.ReportStateChange(PluginID, statuscheck.Init, nil)

	s.db = s.Etcd.NewBroker(keyval.Root)
	database.InitDatabase(s.db)

	s.RegisterModelTypeManagers()

	s.InitRAMCache()

	s.initMgrs()

	if err := s.PreProcessEntityStatus(); err != nil {
		os.Exit(1)
	}

	// the db has been loaded and vpp entries known so now we can clean up the
	// db and remove the vpp agent entries that the controller has managed/created
	if cleanSfcDatastore {
		database.CleanDatastore(controller.SfcControllerConfigPrefix())
		s.CleanVppAgentEntriesFromEtcd()
		s.InitRAMCache()
	}

	// If a startup yaml file is provided, then pull it into the ram cache and write it to the database
	// Note that there may already be an existing database so the policy is that the config yaml
	// file will replace any conflicting entries in the database.
	if sfcConfigFile != "" {

		if yamlConfig, err := s.SfcConfigYamlReadFromFile(sfcConfigFile); err != nil {
			log.Error("error loading config: ", err)
			os.Exit(1)
		} else if err := s.SfcConfigYamlProcessConfig(yamlConfig); err != nil {
			log.Error("error copying config: ", err)
			os.Exit(1)
		}
	}

	log.Infof("Dumping: controller cache: %v", s.ramConfigCache)
	for _, entry := range RegisteredManagers {
		log.Infof("Init: dumping %s ...", entry.modelTypeName)
		entry.mgr.DumpCache()
	}

	return nil
}

func (s *Plugin) initMgrs() {
	for _, entry := range RegisteredManagers {
		log.Infof("initMgrs: initing %s ...", entry.modelTypeName)
		entry.mgr.Init()
	}
}

func (s *Plugin) afterInitMgrs() {
	s.InitSystemHTTPHandler()

	for _, entry := range RegisteredManagers {
		log.Infof("afterInitMgrs: after initing %s ...", entry.modelTypeName)
		entry.mgr.AfterInit()
	}
}

// AfterInit is called after all plugin are init-ed
func (s *Plugin) AfterInit() error {
	log.Info("AfterInit:", PluginID)

	// at this point, plugins are all loaded, all is read in from the database
	// so render the config ... note: resync will ensure etcd is not written to
	// unnecessarily

	RenderTxnConfigStart()
	s.RenderAll()
	RenderTxnConfigEnd()

	s.afterInitMgrs()

	if contivKSREnabled {
		go ctlrPlugin.NetworkPodNodeMapMgr.RunContivKSRNetworkPodToNodeMappingWatcher()
	}

	s.StatusCheck.ReportStateChange(PluginID, statuscheck.OK, nil)

	return nil
}

// RenderAll calls only node and service as the rest are resources used by these
func (s *Plugin) RenderAll() {
	ctlrPlugin.NetworkNodeMgr.RenderAll()
	ctlrPlugin.NetworkServiceMgr.RenderAll()
	//for _, entry := range RegisteredManagers {
	//	log.Infof("RenderAll: initial rendering %s ...", entry.modelTypeName)
	//	entry.mgr.RenderAll()
	//}
}

// InitRAMCache creates the ram cache
func (s *Plugin) InitRAMCache() {

	s.ramConfigCache.IPAMPoolAllocators = nil
	s.ramConfigCache.IPAMPoolAllocators = make(map[string]*ipam.PoolAllocatorType)

	//s.ramConfigCache.NetworkNodeOverlayVniAllocators = nil
	//s.ramConfigCache.VNFServiceMeshVniAllocators = make(map[string]*idapi.VxlanVniAllocatorType)
	//
	//s.ramConfigCache.VNFServiceMeshVxLanAddresses = nil
	//s.ramConfigCache.VNFServiceMeshVxLanAddresses = make(map[string]string)

	s.ramConfigCache.VppEntries = nil
	s.ramConfigCache.VppEntries = make(map[string]*vppagent.KVType)

	s.ramConfigCache.MacAddrAllocator = nil
	s.ramConfigCache.MacAddrAllocator = idapi.NewMacAddrAllocator()

	s.ramConfigCache.MemifIDAllocator = nil
	s.ramConfigCache.MemifIDAllocator = idapi.NewMemifAllocator()

	s.ramConfigCache.InterfaceStates = nil
	s.ramConfigCache.InterfaceStates = make(map[string]*controller.InterfaceStatus)

	for _, entry := range RegisteredManagers {
		log.Infof("InitRAMCache: %s ...", entry.modelTypeName)
		entry.mgr.InitRAMCache()
	}

	s.ramConfigCache.NetworkPodToNodeMap = make(map[string]*NetworkPodToNodeMap, 0)
}

// Close performs close down procedures
func (s *Plugin) Close() error {
	return nil
}

// InitHTTPHandlers registers the handler funcs for CRUD operations
func (s *Plugin) InitSystemHTTPHandler() {

	log.Infof("InitHTTPHandlers: registering ...")

	log.Infof("InitHTTPHandlers: registering GET %s", controller.SfcControllerPrefix())
	ctlrPlugin.HTTPmux.RegisterHTTPHandler(controller.SfcControllerPrefix(), httpSystemGetAllYamlHandler, "GET")
}

// curl -X GET http://localhost:9191/sfc_controller
func httpSystemGetAllYamlHandler(formatter *render.Render) http.HandlerFunc {

	// This routine is a debug dump routine that dumps the config/state of the entire system in yaml format

	return func(w http.ResponseWriter, req *http.Request) {
		log.Debugf("httpSystemGetAllYamlHandler: Method %s, URL: %s", req.Method, req.URL)

		switch req.Method {
		case "GET":
			yaml, err := ctlrPlugin.SfcSystemCacheToYaml()
			if err != nil {
				formatter.JSON(w, http.StatusInternalServerError, struct{ Error string }{err.Error()})

			}
			formatter.Data(w, http.StatusOK, yaml)
		}
	}
}

// PreProcessEntityStatus uses key/type from state to lad vpp entries from etcd
func (s *Plugin) PreProcessEntityStatus() error {

	log.Debugf("PreProcessEntityStatus: processing ipam pool state: num: %d",
		len(s.IpamPoolMgr.ipamPoolCache))
	for _, ipamPool := range s.IpamPoolMgr.ipamPoolCache {
		if ipamPool.Status == nil {
			ipamPool.Status = &controller.IPAMPoolStatus{
				Addresses: make(map[string]string, 0),
			}
		} else {
			if ipamPool.Status.Addresses == nil {
				ipamPool.Status.Addresses = make(map[string]string, 0)
			}
		}
	}

	log.Debugf("PreProcessEntityStatus: processing nodes state: num: %d",
		len(s.NetworkNodeMgr.networkNodeCache))
	for _, nn := range s.NetworkNodeMgr.networkNodeCache {

		ctlrPlugin.IpamPoolMgr.EntityCreate(nn.Metadata.Name, controller.IPAMPoolScopeNode)

		if nn.Status != nil && len(nn.Status.RenderedVppAgentEntries) != 0 {

			log.Debugf("PreProcessEntityStatus: processing node state: %s", nn.Metadata.Name)
			if err := s.LoadVppAgentEntriesFromRenderedVppAgentEntries(nn.Status.RenderedVppAgentEntries); err != nil {
				return err
			}
		}
		if nn.Status != nil && len(nn.Status.Interfaces) != 0 {
			for _, ifStatus := range nn.Status.Interfaces {
				ctlrPlugin.ramConfigCache.InterfaceStates[ifStatus.Name] = ifStatus
				if ifStatus.MemifID > ctlrPlugin.ramConfigCache.MemifIDAllocator.MemifID {
					ctlrPlugin.ramConfigCache.MemifIDAllocator.MemifID = ifStatus.MemifID
				}
				if ifStatus.MacAddrID > ctlrPlugin.ramConfigCache.MacAddrAllocator.MacAddrID {
					ctlrPlugin.ramConfigCache.MacAddrAllocator.MacAddrID = ifStatus.MacAddrID
				}
				UpdateRamCacheAllocatorsForInterfaceStatus(ifStatus, nn.Metadata.Name)
			}
		}
	}

	log.Debugf("PreProcessEntityStatus: processing network services state: num: %d",
		len(s.NetworkServiceMgr.networkServiceCache))
	for _, ns := range s.NetworkServiceMgr.networkServiceCache {

		ctlrPlugin.IpamPoolMgr.EntityCreate(ns.Metadata.Name, controller.IPAMPoolScopeNetworkService)

		if ns.Status != nil && len(ns.Status.RenderedVppAgentEntries) != 0 {
			log.Debugf("PreProcessEntityStatus: processing vnf service state: %s", ns.Metadata.Name)
			if err := s.LoadVppAgentEntriesFromRenderedVppAgentEntries(ns.Status.RenderedVppAgentEntries); err != nil {
				return err
			}
		}
		if ns.Status != nil && len(ns.Status.Interfaces) != 0 {
			for _, ifStatus := range ns.Status.Interfaces {
				ctlrPlugin.ramConfigCache.InterfaceStates[ifStatus.Name] = ifStatus
				if ifStatus.MemifID > ctlrPlugin.ramConfigCache.MemifIDAllocator.MemifID {
					ctlrPlugin.ramConfigCache.MemifIDAllocator.MemifID = ifStatus.MemifID
				}
				if ifStatus.MacAddrID > ctlrPlugin.ramConfigCache.MacAddrAllocator.MacAddrID {
					ctlrPlugin.ramConfigCache.MacAddrAllocator.MacAddrID = ifStatus.MacAddrID
				}
				UpdateRamCacheAllocatorsForInterfaceStatus(ifStatus, ns.Metadata.Name)
			}
		}

	}

	return nil
}

// LoadVppAgentEntriesFromRenderedVppAgentEntries load from etcd
func (s *Plugin) LoadVppAgentEntriesFromRenderedVppAgentEntries(
	vppAgentEntries map[string]*controller.RenderedVppAgentEntry) error {

	log.Debugf("LoadVppAgentEntriesFromRenderedVppAgentEntries: num: %d, %v",
		len(vppAgentEntries), vppAgentEntries)
	for _, vppAgentEntry := range vppAgentEntries {

		vppKVEntry := vppagent.NewKVEntry(vppAgentEntry.VppAgentKey, vppAgentEntry.VppAgentType)
		found, err := vppKVEntry.ReadFromEtcd(s.db)
		if err != nil {
			return err
		}
		if found {
			s.ramConfigCache.VppEntries[vppKVEntry.VppKey] = vppKVEntry
		}
	}

	return nil
}

// CleanVppAgentEntriesFromEtcd load from etcd
func (s *Plugin) CleanVppAgentEntriesFromEtcd() {
	log.Debugf("CleanVppAgentEntriesFromEtcd: removing all vpp keys managed by the controller")
	for _, kvEntry := range s.ramConfigCache.VppEntries {
		database.DeleteFromDatastore(kvEntry.VppKey)
	}
}
