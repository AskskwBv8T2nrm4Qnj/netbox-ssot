package fmc

import (
	"fmt"

	"github.com/src-doo/netbox-ssot/internal/constants"
	"github.com/src-doo/netbox-ssot/internal/netbox/inventory"
	"github.com/src-doo/netbox-ssot/internal/netbox/objects"
	"github.com/src-doo/netbox-ssot/internal/source/common"
	"github.com/src-doo/netbox-ssot/internal/source/fmc/client"
	"github.com/src-doo/netbox-ssot/internal/utils"
)

func (fmcs *FMCSource) syncDevices(nbi *inventory.NetboxInventory) error {
	for deviceUUID, device := range fmcs.Devices {
		deviceName := device.Name
		if deviceName == "" {
			fmcs.Logger.Warningf(fmcs.Ctx, "device with empty name. Skipping...")
			continue
		}
		var deviceSerialNumber string
		if !fmcs.SourceConfig.IgnoreSerialNumbers {
			deviceSerialNumber = device.Metadata.SerialNumber
		}
		deviceModel := device.Model
		if deviceModel == "" {
			fmcs.Logger.Warning(fmcs.Ctx, "model field for device is emptpy. Using fallback model.")
			deviceModel = constants.DefaultModel
		}
		deviceManufacturer, err := nbi.AddManufacturer(fmcs.Ctx, &objects.Manufacturer{
			Name: "Cisco",
			Slug: utils.Slugify("Cisco"),
		})
		if err != nil {
			return fmt.Errorf("add manufacturer: %s", err)
		}
		deviceType, err := nbi.AddDeviceType(fmcs.Ctx, &objects.DeviceType{
			Manufacturer: deviceManufacturer,
			Model:        deviceModel,
			Slug:         utils.Slugify(deviceManufacturer.Name + deviceModel),
		})
		if err != nil {
			return fmt.Errorf("add device type: %s", err)
		}
		deviceTenant, err := common.MatchHostToTenant(
			fmcs.Ctx,
			nbi,
			deviceName,
			fmcs.SourceConfig.HostTenantRelations,
		)
		if err != nil {
			return fmt.Errorf("match host to tenant %s", err)
		}

		// Match host to a role. First test if user provided relations, if not
		// use default firewall role.
		var deviceRole *objects.DeviceRole
		if len(fmcs.SourceConfig.HostRoleRelations) > 0 {
			deviceRole, err = common.MatchHostToRole(
				fmcs.Ctx,
				nbi,
				deviceName,
				fmcs.SourceConfig.HostRoleRelations,
			)
			if err != nil {
				return fmt.Errorf("match host to role: %s", err)
			}
		}
		if deviceRole == nil {
			deviceRole, err = nbi.AddFirewallDeviceRole(fmcs.Ctx)
			if err != nil {
				return fmt.Errorf("add DeviceRole firewall: %s", err)
			}
		}

		deviceSite, err := common.MatchHostToSite(
			fmcs.Ctx,
			nbi,
			deviceName,
			fmcs.SourceConfig.HostSiteRelations,
		)
		if err != nil {
			return fmt.Errorf("match host to site: %s", err)
		}
		devicePlatformName := fmt.Sprintf("FXOS %s", device.SWVersion)
		devicePlatform, err := nbi.AddPlatform(fmcs.Ctx, &objects.Platform{
			Name:         devicePlatformName,
			Slug:         utils.Slugify(devicePlatformName),
			Manufacturer: deviceManufacturer,
		})
		if err != nil {
			return fmt.Errorf("add platform: %s", err)
		}
		NBDevice, err := nbi.AddDevice(fmcs.Ctx, &objects.Device{
			NetboxObject: objects.NetboxObject{
				Description: device.Description,
				Tags:        fmcs.GetSourceTags(),
				CustomFields: map[string]interface{}{
					constants.CustomFieldSourceIDName:     deviceUUID,
					constants.CustomFieldDeviceUUIDName:   deviceUUID,
					constants.CustomFieldHostCPUCoresName: device.Metadata.InventoryData.CPUCores,
					constants.CustomFieldHostMemoryName: fmt.Sprintf(
						"%sMB",
						device.Metadata.InventoryData.MemoryInMB,
					),
				},
			},
			Name:         deviceName,
			Site:         deviceSite,
			DeviceRole:   deviceRole,
			Status:       &objects.DeviceStatusActive,
			DeviceType:   deviceType,
			Tenant:       deviceTenant,
			Platform:     devicePlatform,
			SerialNumber: deviceSerialNumber,
		})
		if err != nil {
			return fmt.Errorf("add device: %s", err)
		}
		err = fmcs.syncPhysicalInterfaces(nbi, NBDevice, deviceUUID)
		if err != nil {
			return fmt.Errorf("sync physical interfaces: %s", err)
		}
		err = fmcs.syncVlanInterfaces(nbi, NBDevice, deviceUUID)
		if err != nil {
			return fmt.Errorf("sync vlan interfaces: %s", err)
		}
		err = fmcs.syncEtherChannelInterfaces(nbi, NBDevice, deviceUUID)
		if err != nil {
			return fmt.Errorf("sync etherchannel interfaces: %s", err)
		}
		// syncSubInterfaces should be called lastly, since it is dependant
		// on other sync functions.
		err = fmcs.syncSubInterfaces(nbi, NBDevice, deviceUUID)
		if err != nil {
			return fmt.Errorf("sync subinterfaces: %s", err)
		}
	}
	return nil
}

// Helper function to extract IP address from the given interface.
// If interface doesn't have an IP address, empty string is returned.
func getIPAddressForIface(ipv4 *client.InterfaceIPv4) string {
	if ipv4 != nil {
		if ipv4.Static != nil {
			if ipv4.Static.Address != "" {
				return fmt.Sprintf(
					"%s/%s",
					ipv4.Static.Address,
					utils.SerializeMask(ipv4.Static.Netmask),
				)
			}
		}
		if ipv4.Dhcp != nil {
			if ipv4.Dhcp.Address != "" {
				addr := fmt.Sprintf(
					"%s/%s",
					ipv4.Dhcp.Address,
					utils.SerializeMask(ipv4.Dhcp.Netmask),
				)
				return addr
			}
		}
	}
	return ""
}

// syncVlanInterfaces syncs vlan interfaces for given device,
// into netbox inventory.
func (fmcs *FMCSource) syncVlanInterfaces(
	nbi *inventory.NetboxInventory,
	nbDevice *objects.Device,
	deviceUUID string,
) error {
	if vlanIfaces, ok := fmcs.DeviceVlanIfaces[deviceUUID]; ok {
		for _, vlanIface := range vlanIfaces {
			// Add vlan
			ifaceTaggedVlans := []*objects.Vlan{}
			if vlanIface.VID != 0 {
				// Match vlan to site
				vlanSite, err := common.MatchVlanToSite(
					fmcs.Ctx,
					nbi,
					vlanIface.Name,
					fmcs.SourceConfig.VlanSiteRelations,
				)
				if err != nil {
					return fmt.Errorf("match vlan to site: %s", err)
				}
				// Match vlan to group
				vlanGroup, err := common.MatchVlanToGroup(
					fmcs.Ctx,
					nbi,
					vlanIface.Name,
					vlanSite,
					fmcs.SourceConfig.VlanGroupRelations,
					fmcs.SourceConfig.VlanGroupSiteRelations,
				)
				if err != nil {
					return fmt.Errorf("match vlan to group: %s", err)
				}
				vlanTenant, err := common.MatchVlanToTenant(
					fmcs.Ctx,
					nbi,
					vlanIface.Name,
					fmcs.SourceConfig.VlanTenantRelations,
				)
				if err != nil {
					return fmt.Errorf("match vlan to tenant: %s", err)
				}
				vlan, err := nbi.AddVlan(fmcs.Ctx, &objects.Vlan{
					NetboxObject: objects.NetboxObject{
						Tags:        fmcs.GetSourceTags(),
						Description: vlanIface.Description,
					},
					Status: &objects.VlanStatusActive,
					Name:   vlanIface.Name,
					Site:   vlanSite,
					Vid:    vlanIface.VID,
					Tenant: vlanTenant,
					Group:  vlanGroup,
				})
				if err != nil {
					return fmt.Errorf("add vlan: %s", err)
				}
				ifaceTaggedVlans = append(ifaceTaggedVlans, vlan)
			}

			NBIface, err := nbi.AddInterface(fmcs.Ctx, &objects.Interface{
				NetboxObject: objects.NetboxObject{
					Description: vlanIface.Description,
					Tags:        fmcs.GetSourceTags(),
					CustomFields: map[string]interface{}{
						constants.CustomFieldSourceIDName: vlanIface.ID,
					},
				},
				Name:        vlanIface.Name,
				Device:      nbDevice,
				Status:      vlanIface.Enabled,
				MTU:         vlanIface.MTU,
				TaggedVlans: ifaceTaggedVlans,
				Type:        &objects.VirtualInterfaceType,
			})
			if err != nil {
				return fmt.Errorf("add vlan interface: %s", err)
			}

			if ipAddress := getIPAddressForIface(vlanIface.IPv4); ipAddress != "" {
				if utils.IsPermittedIPAddress(
					ipAddress,
					fmcs.SourceConfig.PermittedSubnets,
					fmcs.SourceConfig.IgnoredSubnets,
				) {
					dnsName := utils.ReverseLookup(vlanIface.IPv4.Static.Address)
					_, err := nbi.AddIPAddress(fmcs.Ctx, &objects.IPAddress{
						NetboxObject: objects.NetboxObject{
							Tags: fmcs.GetSourceTags(),
							CustomFields: map[string]interface{}{
								constants.CustomFieldArpEntryName: false,
							},
						},
						Address:            ipAddress,
						DNSName:            dnsName,
						AssignedObjectID:   NBIface.ID,
						AssignedObjectType: constants.ContentTypeDcimInterface,
					})
					if err != nil {
						return fmt.Errorf("add ip address")
					}
					// Also add prefix
					prefix, mask, err := utils.GetPrefixAndMaskFromIPAddress(ipAddress)
					if err != nil {
						fmcs.Logger.Debugf(fmcs.Ctx, "extract prefix from address: %s", err)
					} else if mask != constants.MaxIPv4MaskBits {
						var prefixTenant *objects.Tenant
						var prefixVlan *objects.Vlan
						if len(ifaceTaggedVlans) > 0 {
							prefixVlan = ifaceTaggedVlans[0]
							prefixTenant = prefixVlan.Tenant
						}
						_, err = nbi.AddPrefix(fmcs.Ctx, &objects.Prefix{
							Prefix: prefix,
							Tenant: prefixTenant,
							Vlan:   prefixVlan,
						})
						if err != nil {
							return fmt.Errorf("add prefix: %s", err)
						}
					}
				}
			}
			// Add to internal map so we can connect subinterfaces
			// with vlan interfaces.
			fmcs.Name2NBInterface[vlanIface.Name] = NBIface
		}
	}
	return nil
}

// syncPhysicalInterfaces syncs physical interfaces for given device,
// into netbox inventory.
func (fmcs *FMCSource) syncPhysicalInterfaces(
	nbi *inventory.NetboxInventory,
	nbDevice *objects.Device,
	deviceUUID string,
) error {
	if physicalIfaces, ok := fmcs.DevicePhysicalIfaces[deviceUUID]; ok {
		for _, pIface := range physicalIfaces {
			iface := &objects.Interface{
				NetboxObject: objects.NetboxObject{
					Description: pIface.Description,
					Tags:        fmcs.GetSourceTags(),
					CustomFields: map[string]interface{}{
						constants.CustomFieldSourceIDName: pIface.ID,
					},
				},
				Name:   pIface.Name,
				Device: nbDevice,
				Status: pIface.Enabled,
				MTU:    pIface.MTU,
				Type:   &objects.OtherInterfaceType,
			}
			NBIface, err := nbi.AddInterface(fmcs.Ctx, iface)
			if err != nil {
				return fmt.Errorf("add physical interface %+v: %s", iface, err)
			}
			if ipAddr := getIPAddressForIface(pIface.IPv4); ipAddr != "" {
				if utils.IsPermittedIPAddress(
					ipAddr,
					fmcs.SourceConfig.PermittedSubnets,
					fmcs.SourceConfig.IgnoredSubnets,
				) {
					dnsName := utils.ReverseLookup(pIface.IPv4.Static.Address)
					_, err := nbi.AddIPAddress(fmcs.Ctx, &objects.IPAddress{
						NetboxObject: objects.NetboxObject{
							Tags: fmcs.GetSourceTags(),
							CustomFields: map[string]interface{}{
								constants.CustomFieldArpEntryName: false,
							},
						},
						Address:            ipAddr,
						DNSName:            dnsName,
						AssignedObjectID:   NBIface.ID,
						AssignedObjectType: constants.ContentTypeDcimInterface,
					})
					if err != nil {
						return fmt.Errorf("add ip address")
					}
				}
			}
			// Add to internal map so we can connect subinterfaces
			// with physical interfaces.
			fmcs.Name2NBInterface[pIface.Name] = NBIface
		}
	}
	return nil
}

func (fmcs *FMCSource) syncEtherChannelInterfaces(
	nbi *inventory.NetboxInventory,
	nbDevice *objects.Device,
	deviceUUID string,
) error {
	if etherChannelIfaces, ok := fmcs.DeviceEtherChannelIfaces[deviceUUID]; ok {
		for _, eIface := range etherChannelIfaces {
			NBIface, err := nbi.AddInterface(fmcs.Ctx, &objects.Interface{
				NetboxObject: objects.NetboxObject{
					Description: eIface.Description,
					Tags:        fmcs.GetSourceTags(),
					CustomFields: map[string]interface{}{
						constants.CustomFieldSourceIDName: eIface.ID,
					},
				},
				Name:   eIface.Name,
				Device: nbDevice,
				Status: eIface.Enabled,
				MTU:    eIface.MTU,
				Type:   &objects.OtherInterfaceType, // TODO
			})
			if err != nil {
				return fmt.Errorf("add ether channel interface: %s", err)
			}

			if ipAddr := getIPAddressForIface(eIface.IPv4); ipAddr != "" {
				if utils.IsPermittedIPAddress(
					ipAddr,
					fmcs.SourceConfig.PermittedSubnets,
					fmcs.SourceConfig.IgnoredSubnets,
				) {
					dnsName := utils.ReverseLookup(eIface.IPv4.Static.Address)
					_, err := nbi.AddIPAddress(fmcs.Ctx, &objects.IPAddress{
						NetboxObject: objects.NetboxObject{
							Tags: fmcs.GetSourceTags(),
							CustomFields: map[string]interface{}{
								constants.CustomFieldArpEntryName: false,
							},
						},
						Address:            ipAddr,
						DNSName:            dnsName,
						AssignedObjectID:   NBIface.ID,
						AssignedObjectType: constants.ContentTypeDcimInterface,
					})
					if err != nil {
						return fmt.Errorf("add ip address")
					}
				}
			}
			// Add to internal map so we can connect subinterfaces
			// with etherchannel interfaces.
			fmcs.Name2NBInterface[eIface.Name] = NBIface
		}
	}
	return nil
}

// syncSubInterfaces syncs sub interfaces for a given nbDevice and its deviceUUID.
func (fmcs *FMCSource) syncSubInterfaces(
	nbi *inventory.NetboxInventory,
	nbDevice *objects.Device,
	deviceUUID string,
) error {
	if subIfaces, ok := fmcs.DeviceSubIfaces[deviceUUID]; ok {
		for _, subIface := range subIfaces {
			// Add vlan
			ifaceTaggedVlans := []*objects.Vlan{}
			if subIface.VlanID > 1 {
				// Match vlan to site
				vlanSite, err := common.MatchVlanToSite(
					fmcs.Ctx,
					nbi,
					subIface.Name,
					fmcs.SourceConfig.VlanSiteRelations,
				)
				if err != nil {
					return fmt.Errorf("match subiface vlan to site: %s", err)
				}
				// Match vlan to group
				vlanGroup, err := common.MatchVlanToGroup(
					fmcs.Ctx,
					nbi,
					subIface.Name,
					vlanSite,
					fmcs.SourceConfig.VlanGroupRelations,
					fmcs.SourceConfig.VlanGroupSiteRelations,
				)
				if err != nil {
					return fmt.Errorf("match subiface vlan to group: %s", err)
				}
				vlanTenant, err := common.MatchVlanToTenant(
					fmcs.Ctx,
					nbi,
					subIface.Name,
					fmcs.SourceConfig.VlanTenantRelations,
				)
				if err != nil {
					return fmt.Errorf("match subiface vlan to tenant: %s", err)
				}
				vlan, err := nbi.AddVlan(fmcs.Ctx, &objects.Vlan{
					NetboxObject: objects.NetboxObject{
						Tags:        fmcs.GetSourceTags(),
						Description: subIface.Description,
					},
					Status: &objects.VlanStatusActive,
					Name:   subIface.Name,
					Site:   vlanSite,
					Vid:    subIface.VlanID,
					Tenant: vlanTenant,
					Group:  vlanGroup,
				})
				if err != nil {
					return fmt.Errorf("add subiface vlan: %s", err)
				}
				ifaceTaggedVlans = append(ifaceTaggedVlans, vlan)
			}

			parentIface := fmcs.Name2NBInterface[subIface.ParentName]

			NBIface, err := nbi.AddInterface(fmcs.Ctx, &objects.Interface{
				NetboxObject: objects.NetboxObject{
					Description: subIface.Description,
					Tags:        fmcs.GetSourceTags(),
					CustomFields: map[string]interface{}{
						constants.CustomFieldSourceIDName: subIface.ID,
					},
				},
				Name:            subIface.Name,
				ParentInterface: parentIface,
				Device:          nbDevice,
				Status:          subIface.Enabled,
				MTU:             subIface.MTU,
				TaggedVlans:     ifaceTaggedVlans,
				Type:            &objects.VirtualInterfaceType,
			})
			if err != nil {
				return fmt.Errorf("add vlan interface: %s", err)
			}

			if ipAddress := getIPAddressForIface(subIface.IPv4); ipAddress != "" {
				if utils.IsPermittedIPAddress(
					ipAddress,
					fmcs.SourceConfig.PermittedSubnets,
					fmcs.SourceConfig.IgnoredSubnets,
				) {
					dnsName := utils.ReverseLookup(subIface.IPv4.Static.Address)
					_, err := nbi.AddIPAddress(fmcs.Ctx, &objects.IPAddress{
						NetboxObject: objects.NetboxObject{
							Tags: fmcs.GetSourceTags(),
							CustomFields: map[string]interface{}{
								constants.CustomFieldArpEntryName: false,
							},
						},
						Address:            ipAddress,
						DNSName:            dnsName,
						AssignedObjectID:   NBIface.ID,
						AssignedObjectType: constants.ContentTypeDcimInterface,
					})
					if err != nil {
						return fmt.Errorf("add ip address")
					}
					// Also add prefix
					prefix, mask, err := utils.GetPrefixAndMaskFromIPAddress(ipAddress)
					if err != nil {
						fmcs.Logger.Debugf(fmcs.Ctx, "extract prefix from address: %s", err)
					} else if mask != constants.MaxIPv4MaskBits {
						var prefixTenant *objects.Tenant
						var prefixVlan *objects.Vlan
						if len(ifaceTaggedVlans) > 0 {
							prefixVlan = ifaceTaggedVlans[0]
							prefixTenant = prefixVlan.Tenant
						}
						_, err = nbi.AddPrefix(fmcs.Ctx, &objects.Prefix{
							Prefix: prefix,
							Tenant: prefixTenant,
							Vlan:   prefixVlan,
						})
						if err != nil {
							return fmt.Errorf("add prefix: %s", err)
						}
					}
				}
			}
		}
	}
	return nil
}
