package cfgmgr

import (
	"errors"
	"fmt"

	winio "github.com/Microsoft/go-winio/pkg/guid"
	errorspkg "github.com/pkg/errors"
)

//go:generate go run ../../mksyscall_windows.go -output zsyscall_windows.go cfgmgr.go

//sys cmGetDeviceIDListSize(pulLen *uint32, pszFilter *byte, uFlags uint64) (hr error) = cfgmgr32.CM_Get_Device_ID_List_SizeA
//sys cmGetDeviceIDList(pszFilter *byte, buffer *byte, bufferLen uint64, uFlags uint64) (hr error)= cfgmgr32.CM_Get_Device_ID_ListA
//sys cmLocateDevNode(pdnDevInst *uint32, pDeviceID string, uFlags uint64) (hr error) = cfgmgr32.CM_Locate_DevNodeW
//sys cmGetDevNodeProperty(dnDevInst uint32, propertyKey *devPropKey, propertyType *uint64, propertyBuffer *byte, propertyBufferSize *uint64, uFlags uint64) (hr error) = cfgmgr32.CM_Get_DevNode_PropertyW

type devPropKey struct {
	fmtid winio.GUID
	pid   uint64
}

var (
	CM_GETIDLIST_FILTER_NONE               = uint64(0x00000000)
	CM_GETIDLIST_FILTER_ENUMERATOR         = uint64(0x00000001)
	CM_GETIDLIST_FILTER_SERVICE            = uint64(0x00000002)
	CM_GETIDLIST_FILTER_EJECTRELATIONS     = uint64(0x00000004)
	CM_GETIDLIST_FILTER_REMOVALRELATIONS   = uint64(0x00000008)
	CM_GETIDLIST_FILTER_POWERRELATIONS     = uint64(0x00000010)
	CM_GETIDLIST_FILTER_BUSRELATIONS       = uint64(0x00000020)
	CM_GETIDLIST_DONOTGENERATE             = uint64(0x10000040)
	CM_GETIDLIST_FILTER_TRANSPORTRELATIONS = uint64(0x00000080)
	CM_GETIDLIST_FILTER_PRESENT            = uint64(0x00000100)
	CM_GETIDLIST_FILTER_CLASS              = uint64(0x00000200)
	CM_GETIDLIST_FILTER_BITS               = uint64(0x100003FF)

	CM_LOCATE_DEVNODE_NORMAL       = uint64(0x00000000)
	CM_LOCATE_DEVNODE_PHANTOM      = uint64(0x00000001)
	CM_LOCATE_DEVNODE_CANCELREMOVE = uint64(0x00000002)
	CM_LOCATE_DEVNODE_NOVALIDATION = uint64(0x00000004)

	DEVPROP_TYPE_STRING      = uint64(0x00000012)
	DEVPROP_TYPEMOD_LIST     = uint64(0x00002000)
	DEVPROP_TYPE_STRING_LIST = uint64(DEVPROP_TYPE_STRING | DEVPROP_TYPEMOD_LIST)
)

func GetDevPKeyDeviceLocationPaths() (*devPropKey, error) {
	guid, err := winio.FromString("a45c254e-df1c-4efd-8020-67d146a850e0")
	if err != nil {
		return nil, err
	}
	return &devPropKey{
		fmtid: guid,
		pid:   37,
	}, nil
}

func GetDeviceLocationPathsFromIDs(ids []string) ([]string, error) {
	result := []string{}
	devPKeyDeviceLocationPaths, err := GetDevPKeyDeviceLocationPaths()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		var devNodeInst uint32
		fmt.Println(id)
		err = cmLocateDevNode(&devNodeInst, id, CM_LOCATE_DEVNODE_NORMAL)
		if err != nil {
			return nil, errorspkg.Wrapf(err, "failed to locate device node for %s", id)
		}
		propertyType := uint64(0)
		maxBufferSize := uint64(16384)
		propertyBufferSize := maxBufferSize
		var propertyBuffer [16384]byte
		err = cmGetDevNodeProperty(devNodeInst, devPKeyDeviceLocationPaths, &propertyType, &propertyBuffer[0], &propertyBufferSize, 0)
		if err != nil {
			return nil, errorspkg.Wrapf(err, "failed to get location path property from device node for %s with", id)
		}
		if propertyType != DEVPROP_TYPE_STRING_LIST {
			return nil, fmt.Errorf("expected to return property type DEVPROP_TYPE_STRING_LIST %d, instead got %d", DEVPROP_TYPE_STRING_LIST, propertyType)
		}
		if propertyBufferSize > maxBufferSize {
			return nil, fmt.Errorf("location path %s is too long", string(propertyBuffer[:maxBufferSize]))
		}
		result = append(result, string(propertyBuffer[:propertyBufferSize]))
	}

	return result, nil
}

func GetChildrenFromInstanceIDs(parentIDs []string) ([]string, error) {
	var result []string
	for _, id := range parentIDs {
		var devNodeInst uint32
		err := cmLocateDevNode(&devNodeInst, id, CM_LOCATE_DEVNODE_NORMAL)
		if err != nil {
			return nil, err
		}
		pszFilterParentID := []byte(id)
		children, err := getDeviceIDList(&pszFilterParentID[0], CM_GETIDLIST_FILTER_BUSRELATIONS)
		if err != nil {
			return nil, err
		}
		result = append(result, children...)
	}
	return result, nil
}

func GetDeviceIDListAllPresent() ([]string, error) {
	return getDeviceIDList(nil, CM_GETIDLIST_FILTER_PRESENT)
}

func GetDeviceIDListFromEnumerator(pszFilter string) ([]string, error) {
	if pszFilter == "" {
		return nil, errors.New("GetDeviceIDListFromEnumerator enumerator must not be empty")
	}
	pszFilterByte := []byte(pszFilter)
	pszFilterBytePtr := &pszFilterByte[0]
	return getDeviceIDList(pszFilterBytePtr, CM_GETIDLIST_FILTER_ENUMERATOR)
}

func getDeviceIDList(pszFilter *byte, ulFlags uint64) ([]string, error) {
	listLength := uint32(0)
	err := cmGetDeviceIDListSize(&listLength, pszFilter, ulFlags)
	if err != nil {
		return nil, err
	}
	if listLength == 0 {
		return []string{}, nil
	}
	var buf = make([]byte, uint64(listLength))
	err = cmGetDeviceIDList(pszFilter, &buf[0], uint64(listLength), ulFlags)
	if err != nil {
		return nil, err
	}

	var result []string
	prev := 0
	for i, c := range buf {
		if c == 0 {
			// this is a null character, we've seen a string
			if buf[prev] != 0 {
				result = append(result, string(buf[prev:i]))
			}
			// don't include the null character
			prev = i + 1
		}
	}

	return result, nil
}
