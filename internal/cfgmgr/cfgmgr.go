package cfgmgr

//go:generate go run ../../mksyscall_windows.go -output zsyscall_windows.go cfgmgr.go

//sys cmGetDeviceIDListSize(pulLen *uint32, pszFilter uintptr, uFlags uint64) (hr error) = cfgmgr32.CM_Get_Device_ID_List_SizeA
//sys cmGetDeviceIDList(pszFilter uintptr, buffer *byte, bufferLen uint64, uFlags uint64) (hr error)= cfgmgr32.CM_Get_Device_ID_ListA

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
)

func GetDeviceIDListPresent() ([]string, error) {
	listLength := uint32(0)
	err := cmGetDeviceIDListSize(&listLength, 0, CM_GETIDLIST_FILTER_PRESENT)
	if err != nil {
		return []string{}, err
	}

	var buf = make([]byte, uint64(listLength))
	err = cmGetDeviceIDList(0, &buf[0], uint64(listLength), CM_GETIDLIST_FILTER_PRESENT)
	if err != nil {
		return []string{}, err
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

func wcslen(buf []uint16) int {
	for i, c := range buf {
		if c == 0 {
			return i
		}
	}
	return 0
}
