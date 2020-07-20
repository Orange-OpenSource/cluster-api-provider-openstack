/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package networking

import (
	"fmt"

	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	infrav1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha3"
)

// ReconcileVIPPort handles the creation of an unmanaged VIPPort used as entry point for the
// cluster. It is up to cloud-init scripts to bind control nodes to this port.
func (s *Service) ReconcileVIPPort(clusterName string, openStackCluster *infrav1.OpenStackCluster) error {
	if openStackCluster.Status.Network == nil || openStackCluster.Status.Network.Subnet == nil || openStackCluster.Status.Network.Subnet.ID == "" {
		s.logger.V(4).Info("No need to reconcile portVIP since no subnet exists.")
		return nil
	}
	if openStackCluster.Spec.ExternalNetworkID == "" {
		s.logger.V(3).Info("No need to create portVIP, due to missing ExternalNetworkID.")
		return nil
	}
	portName := fmt.Sprintf("%s-cluster-%s", networkPrefix, clusterName)
	s.logger.Info("Reconciling VIP port", "name", portName)
	allPages, err := ports.List(s.client, ports.ListOpts{
		Name: portName,
	}).AllPages()
	if err != nil {
		return err
	}

	portsList, err := ports.ExtractPorts(allPages)
	if err != nil {
		return err
	}
	var port ports.Port
	if len(portsList) == 0 {
		options := ports.CreateOpts{
			Name:      portName,
			NetworkID: openStackCluster.Status.Network.ID,
			FixedIPs: []ports.IP{
				{
					SubnetID:  openStackCluster.Status.Network.Subnet.ID,
					IPAddress: openStackCluster.Spec.ControlPlaneInternalIP,
				},
			},
		}
		newPort, err := ports.Create(s.client, options).Extract()
		if err != nil {
			return fmt.Errorf("error allocating VIP port: %s", err)
		}
		port = *newPort
	} else {
		port = portsList[0]
	}
	// floating ip
	fp, err := checkIfFloatingIPExists(s.client, openStackCluster.Spec.APIServerLoadBalancerFloatingIP)
	if err != nil {
		return err
	}

	if fp == nil {
		s.logger.Info("Creating floating ip", "ip", openStackCluster.Spec.APIServerLoadBalancerFloatingIP)
		fpCreateOpts := &floatingips.CreateOpts{
			FloatingIP:        openStackCluster.Spec.APIServerLoadBalancerFloatingIP,
			FloatingNetworkID: openStackCluster.Spec.ExternalNetworkID,
		}
		fp, err = floatingips.Create(s.client, fpCreateOpts).Extract()
		if err != nil {
			return fmt.Errorf("error allocating floating IP: %s", err)
		}
	}

	// associate floating ip
	s.logger.Info("Associating floating ip", "ip", openStackCluster.Spec.APIServerLoadBalancerFloatingIP)
	fpUpdateOpts := &floatingips.UpdateOpts{
		PortID: &port.ID,
	}
	fp, err = floatingips.Update(s.client, fp.ID, fpUpdateOpts).Extract()
	if err != nil {
		return fmt.Errorf("error allocating floating IP: %s", err)
	}
	openStackCluster.Status.Network.UnmanagedPort = &infrav1.UnmanagedPort{
		Name: port.Name,
		ID:   port.ID,
		IP:   fp.FloatingIP,
	}
	return nil
}

// DeleteVIPPort deletes the port representing the internal VIP of the cluster if it was used.
func (s *Service) DeleteVIPPort(unmanagedPort *infrav1.UnmanagedPort) error {
	if unmanagedPort == nil {
		return nil
	}
	res := ports.Delete(s.client, unmanagedPort.ID)
	if res.Err != nil {
		return fmt.Errorf("error while deleting port: %s", res.Err)
	}
	return nil
}
