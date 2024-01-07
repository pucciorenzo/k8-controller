package monitoring

import (
	"context"
	"fmt"
	"net"
	"syscall"

	"github.com/vishvananda/netlink"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// Options defines the options for the monitoring.
type Options struct {
	Link  bool
	Addr  bool
	Route bool
}

// StartMonitoring starts the monitoring of the network interfaces.
// If there is a change in the network interfaces, it will send a message to the channel.
// With the options, you can choose to monitor only the link, address, or route changes.
func StartMonitoring(ctx context.Context, ch chan event.GenericEvent, options Options) {
	// Create channels to receive notifications for link, address, and route changes
	chLink := make(chan netlink.LinkUpdate)
	doneLink := make(chan struct{})
	defer close(doneLink)

	chAddr := make(chan netlink.AddrUpdate)
	doneAddr := make(chan struct{})
	defer close(doneAddr)

	chRoute := make(chan netlink.RouteUpdate)
	doneRoute := make(chan struct{})
	defer close(doneRoute)

	// Subscribe to the address updates
	if err := netlink.AddrSubscribe(chAddr, doneAddr); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Subscribe to the link updates
	if err := netlink.LinkSubscribe(chLink, doneLink); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Subscribe to the route updates
	if err := netlink.RouteSubscribe(chRoute, doneRoute); err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Create maps to keep track of interfaces and newly created interfaces
	newlyCreated := make(map[string]bool)
	interfaces := make(map[string]bool)

	// Get the list of existing links and add them to the interfaces map
	links, err := netlink.LinkList()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	for _, link := range links {
		interfaces[link.Attrs().Name] = true
	}

	// Start an infinite loop to handle the notifications
	for {
		select {
		case updateLink := <-chLink:
			if options.Link {
				handleLinkUpdate(&updateLink, interfaces, newlyCreated, ch)
			}
		case updateAddr := <-chAddr:
			if options.Addr {
				handleAddrUpdate(&updateAddr, ch)
			}
		case updateRoute := <-chRoute:
			if options.Route {
				handleRouteUpdate(&updateRoute, ch)
			}
		// TODO ask if it's ok
		case <-ctx.Done():
			fmt.Println("Monitoring stopped")
			return
		}
	}
}

func handleLinkUpdate(updateLink *netlink.LinkUpdate, interfaces map[string]bool,
	newlyCreated map[string]bool, ch chan<- event.GenericEvent) {
	if updateLink.Header.Type == syscall.RTM_DELLINK {
		// Link has been removed
		fmt.Println("Interface removed:", updateLink.Link.Attrs().Name)
		delete(interfaces, updateLink.Link.Attrs().Name)
		delete(newlyCreated, updateLink.Link.Attrs().Name)
	} else if !interfaces[updateLink.Link.Attrs().Name] && updateLink.Header.Type == syscall.RTM_NEWLINK {
		// New link has been added
		fmt.Println("Interface added")
		interfaces[updateLink.Link.Attrs().Name] = true
		newlyCreated[updateLink.Link.Attrs().Name] = true
	} else if updateLink.Header.Type == syscall.RTM_NEWLINK {
		// Link has been modified
		if updateLink.Link.Attrs().Flags&net.FlagUp != 0 {
			fmt.Println("Interface", updateLink.Link.Attrs().Name, "is up")
			delete(newlyCreated, updateLink.Link.Attrs().Name)
		} else if !newlyCreated[updateLink.Link.Attrs().Name] {
			fmt.Println("Interface", updateLink.Link.Attrs().Name, "is down")
		}
	}
	send(ch)
}

func handleAddrUpdate(updateAddr *netlink.AddrUpdate, ch chan<- event.GenericEvent) {
	iface, err := net.InterfaceByIndex(updateAddr.LinkIndex) // Change to pass the error to the caller
	if err != nil {
		fmt.Println("Address (", updateAddr.LinkAddress.IP, ") removed from the deleted interface")
		return
	}
	if updateAddr.NewAddr {
		// New address has been added
		fmt.Println("New address (", updateAddr.LinkAddress.IP, ") added to the interface:", iface.Name)
		send(ch)
	} else {
		// Address has been removed
		fmt.Println("Address (", updateAddr.LinkAddress.IP, ") removed from the interface:", iface.Name)
		send(ch)
	}
}

func handleRouteUpdate(updateRoute *netlink.RouteUpdate, ch chan<- event.GenericEvent) {
	if updateRoute.Type == syscall.RTM_NEWROUTE {
		// New route has been added
		fmt.Println("New route added:", updateRoute.Route.Dst)
	} else if updateRoute.Type == syscall.RTM_DELROUTE {
		// Route has been removed
		fmt.Println("Route removed:", updateRoute.Route.Dst)
	}
	send(ch)
}

// Send a channel with generic event type.
func send(ch chan<- event.GenericEvent) {
	// Triggers a new reconcile
	ge := event.GenericEvent{}
	ch <- ge
}
