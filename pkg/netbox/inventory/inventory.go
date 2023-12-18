package inventory

import (
	"fmt"

	"github.com/bl4ko/netbox-ssot/pkg/logger"
	"github.com/bl4ko/netbox-ssot/pkg/netbox/common"
	"github.com/bl4ko/netbox-ssot/pkg/netbox/dcim"
	"github.com/bl4ko/netbox-ssot/pkg/netbox/extras"
	"github.com/bl4ko/netbox-ssot/pkg/netbox/service"
	"github.com/bl4ko/netbox-ssot/pkg/netbox/tenancy"
	"github.com/bl4ko/netbox-ssot/pkg/netbox/virtualization"
	"github.com/bl4ko/netbox-ssot/pkg/parser"
)

// NetBoxInventory is a singleton class to manage a inventory of NetBoxObject objects
type NetBoxInventory struct {
	// Logger is the logger used for logging messages
	Logger *logger.Logger
	// NetboxConfig is the NetBox configuration
	NetboxConfig *parser.NetboxConfig
	// NetboxApi is the NetBox API object, for communicating with the NetBox API
	NetboxApi *service.NetboxAPI
	// Tags is a list of all tags in the netbox inventory
	Tags []*common.Tag
	// SitesIndexByName is a map of all sites in the Netbox's inventory, indexed by their name
	SitesIndexByName map[string]*common.Site
	// ManufacturersIndexByName is a map of all manufacturers in the Netbox's inventory, indexed by their name
	ManufacturersIndexByName map[string]*common.Manufacturer
	// PlatformsIndexByName is a map of all platforms in the Netbox's inventory, indexed by their name
	PlatformsIndexByName map[string]*common.Platform
	// TenantsIndexByName is a map of all tenants in the Netbox's inventory, indexed by their name
	TenantsIndexByName map[string]*tenancy.Tenant
	// DeviceTypesIndexByModel is a map of all device types in the Netbox's inventory, indexed by their model
	DeviceTypesIndexByModel map[string]*dcim.DeviceType
	// DevicesIndexByName is a map of all devices in the Netbox's inventory, indexed by their name
	DevicesIndexByName map[string]*dcim.Device
	// ClusterGroupsIndexByName is a map of all cluster groups in the Netbox's inventory, indexed by their name
	ClusterGroupsIndexByName map[string]*virtualization.ClusterGroup
	// ClusterTypesIndexByName is a map of all cluster types in the Netbox's inventory, indexed by their name
	ClusterTypesIndexByName map[string]*virtualization.ClusterType
	// ClustersIndexByName is a map of all clusters in the Netbox's inventory, indexed by their name
	ClustersIndexByName map[string]*virtualization.Cluster
	// Netbox's Device Roles is a map of all device roles in the inventory, indexed by name
	DeviceRolesIndexByName map[string]*dcim.DeviceRole
	// CustomFieldsIndexByName is a map of all custom fields in the inventory, indexed by name
	CustomFieldsIndexByName map[string]*extras.CustomField

	// Orphan manager is a map of { "devices: [device_id1, device_id2, ...], "cluster_groups": [cluster_group_id1, cluster_group_id2, ..."}, to store which objects have been created by netbox-ssot and can be deleted because they are not available in the source anymore
	OrphanManager map[string][]int
	// Tag used by netbox-ssot to mark devices that are managed by it
	SsotTag *common.Tag
}

// Func string representation
func (nbi NetBoxInventory) String() string {
	return fmt.Sprintf("NetBoxInventory{Logger: %+v, NetboxConfig: %+v...}", nbi.Logger, nbi.NetboxConfig)
}

// NewNetboxInventory creates a new NetBoxInventory object.
// It takes a logger and a NetboxConfig as parameters, and returns a pointer to the newly created NetBoxInventory.
// The logger is used for logging messages, and the NetboxConfig is used to configure the NetBoxInventory.
func NewNetboxInventory(logger *logger.Logger, nbConfig *parser.NetboxConfig) *NetBoxInventory {
	nbi := &NetBoxInventory{Logger: logger, NetboxConfig: nbConfig}
	return nbi
}

// Init function that initialises the NetBoxInventory object with objects from NetBox
func (netboxInventory *NetBoxInventory) Init() error {
	baseURL := fmt.Sprintf("%s://%s:%d", netboxInventory.NetboxConfig.HTTPScheme, netboxInventory.NetboxConfig.Hostname, netboxInventory.NetboxConfig.Port)

	netboxInventory.Logger.Debug("Initialising NetBox API with baseURL: ", baseURL)
	netboxInventory.NetboxApi = service.NewNetBoxAPI(netboxInventory.Logger, baseURL, netboxInventory.NetboxConfig.ApiToken, netboxInventory.NetboxConfig.ValidateCert)

	err := netboxInventory.InitTags()
	if err != nil {
		return err
	}
	err = netboxInventory.InitTenants()
	if err != nil {
		return err
	}
	err = netboxInventory.InitSites()
	if err != nil {
		return err
	}
	err = netboxInventory.InitManufacturers()
	if err != nil {
		return err
	}
	err = netboxInventory.InitPlatforms()
	if err != nil {
		return err
	}
	err = netboxInventory.InitDevices()
	if err != nil {
		return err
	}
	err = netboxInventory.InitDeviceRoles()
	if err != nil {
		return err
	}
	// init server device role which is required for separation of device object into servers
	err = netboxInventory.InitServerDeviceRole()
	if err != nil {
		return err
	}
	err = netboxInventory.InitDeviceTypes()
	if err != nil {
		return err
	}
	// init custom fields. Custom fields can be used for devices to add physical cores and memory to each device representing server.
	err = netboxInventory.InitCustomFields()
	if err != nil {
		return err
	}
	err = netboxInventory.InitServerCustomFields()
	if err != nil {
		return err
	}
	err = netboxInventory.InitClusterGroups()
	if err != nil {
		return err
	}
	err = netboxInventory.InitClusterTypes()
	if err != nil {
		return err
	}
	err = netboxInventory.InitClusters()
	if err != nil {
		return err
	}
	return nil
}
