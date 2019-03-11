package network

import (
	"fmt"
	"github.com/coreos/go-iptables/iptables"
	"github.com/songxinjianqwe/capsule/libcapsule/util/exception"
	"github.com/vishvananda/netlink"
	"net"
	"strings"
)

const Masquerade = "-t nat -A POSTROUTING -s %s -o %s -j MASQUERADE"

type BridgeNetworkDriver struct {
}

func (driver *BridgeNetworkDriver) Name() string {
	return "bridge"
}

func (driver *BridgeNetworkDriver) Create(subnet string, bridgeName string) (*Network, error) {
	_, ipRange, err := net.ParseCIDR(subnet)
	network := &Network{
		Name:    bridgeName,
		IpRange: ipRange,
		Driver:  driver.Name(),
	}
	// subnet的格式是192.168.1.2/24，parseCIDR的第一个返回值是IP地址,192.168.1.2，第二个返回值是IPNet类型，192.168.1.0/24
	if err != nil {
		return nil, err
	}
	// 1.创建bridge
	if err := createBridgeInterface(bridgeName); err != nil {
		return nil, exception.NewGenericErrorWithContext(err, exception.SystemError, "create bridge")
	}

	// 2.设置Bridge的IP地址和路由
	if err := setInterfaceIPAndRoute(bridgeName, subnet); err != nil {
		return nil, exception.NewGenericErrorWithContext(err, exception.SystemError, "set bridge ip and route")
	}

	// 3.启动Bridge
	if err := setInterfaceUp(bridgeName); err != nil {
		return nil, exception.NewGenericErrorWithContext(err, exception.SystemError, "set bridge UP")
	}

	// 4.设置iptables SNAT规则（MASQUERADE）
	if err := setupIPTablesMasquerade(bridgeName, ipRange); err != nil {
		return nil, exception.NewGenericErrorWithContext(err, exception.SystemError, "set iptables SNAT MASQUERADE RULE")
	}
	return network, nil
}

func (driver *BridgeNetworkDriver) Load(name string) (*Network, error) {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	//  `ip addr show`.
	addrs, err := netlink.AddrList(iface, netlink.FAMILY_ALL)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("addresses not found")
	}
	var bridgeAddr *net.IPNet
	for _, addr := range addrs {
		if addr.Label == name {
			bridgeAddr = addr.IPNet
			break
		}
	}
	if bridgeAddr == nil {
		return nil, fmt.Errorf("addresses not found")
	}
	return &Network{
		Name:    name,
		Driver:  driver.Name(),
		IpRange: bridgeAddr,
	}, nil
}

func (driver *BridgeNetworkDriver) Delete(name string) error {
	network, err := driver.Load(name)
	if err != nil {
		return err
	}
	// 删除SNAT规则
	tables, err := iptables.New()
	if err := tables.Delete(
		"nat",
		"POSTROUTING",
		getSNATRuleSpec(network.Name, network.IpRange)...,
	); err != nil {
		return err
	}
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	if err := netlink.LinkDel(iface); err != nil {
		return err
	}
	return nil
}

func (driver *BridgeNetworkDriver) Connect(endpointId string, networkName string, portMappings []string) (*Endpoint, error) {
	network, err := LoadNetwork(driver.Name(), networkName)
	if err != nil {
		return nil, err
	}
	allocator, err := LoadIPAllocator()
	if err != nil {
		return nil, err
	}
	ip, err := allocator.Allocate(network.IpRange)
	if err != nil {
		return nil, err
	}
	endpoint := &Endpoint{
		ID:           endpointId,
		Network:      network,
		IpAddress:    ip,
		PortMappings: portMappings,
	}
	// connect

	// config ip address and route

	// config port mapping
	return endpoint, nil
}

func (driver *BridgeNetworkDriver) Disconnect(endpoint *Endpoint) error {
	panic("implement me")
}

// ******************************************************************************************
// util
// ******************************************************************************************

func createBridgeInterface(name string) error {
	if _, err := net.InterfaceByName(name); err == nil || !strings.Contains(err.Error(), "no such network interface") {
		return fmt.Errorf("brigde name %s exists", name)
	}
	linkAttrs := netlink.NewLinkAttrs()
	linkAttrs.Name = name
	br := &netlink.Bridge{
		LinkAttrs: linkAttrs,
	}
	if err := netlink.LinkAdd(br); err != nil {
		return err
	}
	return nil
}

// SNAT MASQUERADE
func setupIPTablesMasquerade(name string, subnet *net.IPNet) error {
	tables, err := iptables.New()
	if err != nil {
		return err
	}
	// iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE
	if err := tables.Append(
		"nat",
		"POSTROUTING", getSNATRuleSpec(name, subnet)...); err != nil {
		return err
	}
	return nil
}

func getSNATRuleSpec(name string, subnet *net.IPNet) []string {
	return []string{
		fmt.Sprintf("-s %s", subnet.String()),
		fmt.Sprintf("-o %s", name),
		fmt.Sprintf("-j MASQUERADE"),
	}
}

// 启用
func setInterfaceUp(name string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	// `ip link set $link up`
	return netlink.LinkSetUp(iface)
}

// 设置网络接口的IP和路由
func setInterfaceIPAndRoute(name string, subnet string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	_, ipRange, err := net.ParseCIDR(subnet)
	// ip addr add xxx
	// 做了两件事：
	// 1、配置了网络接口的IP地址(gatewayIP)
	// 2、配置了路由表，将来自该网段的网络请求转发到这个网络接口上
	addr := &netlink.Addr{
		IPNet: ipRange}
	// `ip addr add $addr dev $link`
	return netlink.AddrAdd(iface, addr)
}