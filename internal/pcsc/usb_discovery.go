package pcsc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const usbSmartCardInterfaceClass = "0b"

// discoverUSBSmartCardReaders finds physical USB CCID interfaces directly in
// sysfs. It is a diagnostic fallback; APDU access still goes through pcscd.
func discoverUSBSmartCardReaders(sysRoot, issue string) []Reader {
	usbRoot := filepath.Join(filepath.Clean(sysRoot), "bus", "usb", "devices")
	entries, err := os.ReadDir(usbRoot)
	if err != nil {
		return nil
	}
	deviceNames := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		class := strings.ToLower(readTrimmedFile(filepath.Join(usbRoot, name, "bInterfaceClass")))
		if deviceName, ok := smartCardUSBDeviceName(name, class); ok {
			deviceNames[deviceName] = struct{}{}
		}
	}
	result := make([]Reader, 0, len(deviceNames))
	for deviceName := range deviceNames {
		path := filepath.Join(usbRoot, deviceName)
		vendorID := strings.ToLower(readTrimmedFile(filepath.Join(path, "idVendor")))
		productID := strings.ToLower(readTrimmedFile(filepath.Join(path, "idProduct")))
		if vendorID == "" || productID == "" {
			continue
		}
		product := readTrimmedFile(filepath.Join(path, "product"))
		if product == "" {
			product = fmt.Sprintf("USB smart card reader %s:%s", vendorID, productID)
		}
		result = append(result, Reader{
			Name: product, USBPath: deviceName,
			VendorID: vendorID, ProductID: productID,
			Manufacturer: readTrimmedFile(filepath.Join(path, "manufacturer")),
			Product:      product, DiscoveryIssue: issue,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].USBPath < result[j].USBPath })
	return result
}

func smartCardUSBDeviceName(interfaceName, class string) (string, bool) {
	deviceName, _, interfaceEntry := strings.Cut(interfaceName, ":")
	return deviceName, interfaceEntry && deviceName != "" && strings.EqualFold(strings.TrimSpace(class), usbSmartCardInterfaceClass)
}

func readTrimmedFile(path string) string {
	value, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func mergePCSCAndUSBReaders(readers, physical []Reader) []Reader {
	if len(readers) == 1 && len(physical) == 1 && strings.HasPrefix(readers[0].USBPath, "pcsc:") {
		readers[0] = enrichPCSCReader(readers[0], physical[0])
		return readers
	}
	matchedPhysical := make(map[string]bool, len(physical))
	for i := range readers {
		for _, usbReader := range physical {
			if readers[i].USBPath == usbReader.USBPath {
				readers[i] = enrichPCSCReader(readers[i], usbReader)
				matchedPhysical[usbReader.USBPath] = true
			}
		}
	}
	// Secondary pass: if any pcsc reader is still prefixed with pcsc: (unresolved sysfs USB path),
	// match with unmatched physical readers by VendorID/ProductID or if 1:1 remaining.
	var remainingPhysical []Reader
	for _, p := range physical {
		if !matchedPhysical[p.USBPath] {
			remainingPhysical = append(remainingPhysical, p)
		}
	}
	for i := range readers {
		if strings.HasPrefix(readers[i].USBPath, "pcsc:") && len(remainingPhysical) == 1 {
			readers[i] = enrichPCSCReader(readers[i], remainingPhysical[0])
			matchedPhysical[remainingPhysical[0].USBPath] = true
			remainingPhysical = nil
			break
		}
	}
	for _, usbReader := range physical {
		if !matchedPhysical[usbReader.USBPath] {
			readers = append(readers, usbReader)
		}
	}
	return readers
}

// filterVirtualPCDReaders removes software-emulated vsmartcard endpoints from
// USB SIM-reader discovery. VMware/Ubuntu commonly installs four readers named
// "Virtual PCD 00 00" through "00 03" even though no USB reader exists. They
// have only a pcsc: fallback path and must not become managed hardware or
// consume the device quota. A real USB CCID reader always has a sysfs USB path
// after enrichment and is therefore retained even if it has an unusual name.
func filterVirtualPCDReaders(readers []Reader) []Reader {
	filtered := readers[:0]
	for _, reader := range readers {
		fallbackPath := strings.HasPrefix(strings.ToLower(strings.TrimSpace(reader.USBPath)), "pcsc:")
		name := strings.ToLower(strings.TrimSpace(reader.Name))
		if fallbackPath && (name == "virtual pcd" || strings.HasPrefix(name, "virtual pcd ")) {
			continue
		}
		filtered = append(filtered, reader)
	}
	return filtered
}

func enrichPCSCReader(reader, physical Reader) Reader {
	reader.USBPath = physical.USBPath
	reader.VendorID = physical.VendorID
	reader.ProductID = physical.ProductID
	reader.Manufacturer = physical.Manufacturer
	if reader.Product == "" {
		reader.Product = physical.Product
	}
	reader.DiscoveryIssue = ""
	return reader
}
