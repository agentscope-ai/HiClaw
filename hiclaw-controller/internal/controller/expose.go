package controller

import (
	"fmt"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
)

// domainForExpose generates the auto domain name for a worker's exposed port.
func domainForExpose(workerName string, port int) string {
	return fmt.Sprintf("worker-%s-%d-local.hiclaw.io", workerName, port)
}

// serviceSourceName generates the Higress service source name.
func serviceSourceName(workerName string, port int) string {
	return fmt.Sprintf("worker-%s-%d", workerName, port)
}

// routeName generates the Higress route name.
func routeName(workerName string, port int) string {
	return fmt.Sprintf("worker-%s-%d", workerName, port)
}

// containerDNSName returns the FQDN for a worker container that Higress can resolve.
// Worker containers are created with a network alias "{name}.local" on hiclaw-net,
// so Higress can resolve this as a DNS service source domain.
func containerDNSName(workerName string) string {
	return fmt.Sprintf("%s.local", workerName)
}

// ReconcileExpose compares desired expose ports with current status, creates new
// Higress resources for added ports, and removes resources for deleted ports.
// Returns the new ExposedPortStatus list.
func ReconcileExpose(hc *HigressClient, workerName string, desired []v1beta1.ExposePort, current []v1beta1.ExposedPortStatus) ([]v1beta1.ExposedPortStatus, error) {
	if hc == nil {
		return current, nil
	}

	// Build lookup maps
	desiredSet := make(map[int]v1beta1.ExposePort)
	for _, ep := range desired {
		desiredSet[ep.Port] = ep
	}
	currentSet := make(map[int]v1beta1.ExposedPortStatus)
	for _, ep := range current {
		currentSet[ep.Port] = ep
	}

	var result []v1beta1.ExposedPortStatus
	var firstErr error

	// Create resources for new ports
	for _, ep := range desired {
		if _, exists := currentSet[ep.Port]; exists {
			// Already exposed, keep it
			result = append(result, currentSet[ep.Port])
			continue
		}

		domain := domainForExpose(workerName, ep.Port)
		svcSrc := serviceSourceName(workerName, ep.Port)
		route := routeName(workerName, ep.Port)
		dnsDomain := containerDNSName(workerName)

		if err := hc.EnsureDomain(domain); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("expose port %d: %w", ep.Port, err)
			}
			continue
		}
		if err := hc.EnsureServiceSource(svcSrc, dnsDomain, ep.Port); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("expose port %d: %w", ep.Port, err)
			}
			continue
		}
		if err := hc.EnsureRoute(route, []string{domain}, svcSrc+".dns", ep.Port); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("expose port %d: %w", ep.Port, err)
			}
			continue
		}

		result = append(result, v1beta1.ExposedPortStatus{
			Port:   ep.Port,
			Domain: domain,
		})
	}

	// Delete resources for removed ports
	for _, ep := range current {
		if _, stillDesired := desiredSet[ep.Port]; stillDesired {
			continue
		}

		route := routeName(workerName, ep.Port)
		svcSrc := serviceSourceName(workerName, ep.Port)
		domain := ep.Domain

		if err := hc.DeleteRoute(route); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unexpose port %d: %w", ep.Port, err)
			}
		}
		if err := hc.DeleteServiceSource(svcSrc); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unexpose port %d: %w", ep.Port, err)
			}
		}
		if err := hc.DeleteDomain(domain); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unexpose port %d: %w", ep.Port, err)
			}
		}
	}

	return result, firstErr
}
