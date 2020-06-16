package winapi

import (
	"errors"
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/Microsoft/go-winio/pkg/guid"
)

//go:generate go run ..\..\mksyscall_windows.go -output zsyscall_windows.go cfgmgr.go kernel32.go ntdll.go

type IOStatusBlock struct {
	Status, Information uintptr
}

type ObjectAttributes struct {
	Length             uintptr
	RootDirectory      uintptr
	ObjectName         uintptr
	Attributes         uintptr
	SecurityDescriptor uintptr
	SecurityQoS        uintptr
}

type UnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        uintptr
}

type ObjectDirectoryInformation struct {
	Name     UnicodeString
	TypeName UnicodeString
}

type FileLinkInformation struct {
	ReplaceIfExists bool
	RootDirectory   uintptr
	FileNameLength  uint32
	FileName        [1]uint16
}

type FileDispositionInformationEx struct {
	Flags uintptr
}

type DevPropKey struct {
	Fmtid guid.GUID
	Pid   uint32
}

const (
	FileLinkInformationClass          = 11
	FileDispositionInformationExClass = 64

	FILE_READ_ATTRIBUTES  = 0x0080
	FILE_WRITE_ATTRIBUTES = 0x0100
	DELETE                = 0x10000

	FILE_OPEN   = 1
	FILE_CREATE = 2

	FILE_LIST_DIRECTORY          = 0x00000001
	FILE_DIRECTORY_FILE          = 0x00000001
	FILE_SYNCHRONOUS_IO_NONALERT = 0x00000020
	FILE_DELETE_ON_CLOSE         = 0x00001000
	FILE_OPEN_FOR_BACKUP_INTENT  = 0x00004000
	FILE_OPEN_REPARSE_POINT      = 0x00200000

	FILE_DISPOSITION_DELETE = 0x00000001

	OBJ_DONT_REPARSE = 0x1000

	STATUS_REPARSE_POINT_ENCOUNTERED = 0xC000050B
	STATUS_MORE_ENTRIES              = 0x105
	STATUS_NO_MORE_ENTRIES           = 0x8000001a

	ERROR_NO_MORE_ITEMS = 0x103
)

func NTSuccess(status uint32) bool {
	return status == 0
}

//String converts a UnicodeString to a golang string
func (uni UnicodeString) String() string {
	p := (*[0xffff]uint16)(unsafe.Pointer(uni.Buffer))

	// UnicodeString is not guaranteed to be null terminated, therefore
	// use the UnicodeString's Length field
	return syscall.UTF16ToString(p[:uni.Length+1])
}

// NewUnicodeString allocates a new UnicodeString and copies `s` into
// the buffer of the new UnicodeString. The address of the heap allocated memory for
// the UnicodeString is stored in `upathBuffer`.
// It is the caller's responsibility to free the memory pointed to by `upathBuffer`
// when no longer in use.
func NewUnicodeString(s string, upathBuffer *uintptr) (*UnicodeString, error) {
	ws, err := NTWidePathString(s)
	if err != nil {
		return nil, err
	}

	*upathBuffer = LocalAlloc(0, int(unsafe.Sizeof(UnicodeString{}))+len(ws)*2)

	upath := (*UnicodeString)(unsafe.Pointer(*upathBuffer))
	upath.Length = uint16(len(ws) * 2)
	upath.MaximumLength = upath.Length
	upath.Buffer = *upathBuffer + unsafe.Sizeof(*upath)
	copy((*[32768]uint16)(unsafe.Pointer(upath.Buffer))[:], ws)
	return upath, nil
}

// NTWidePathString converts a golang file path string to an NT wide
// string
func NTWidePathString(s string) ([]uint16, error) {
	path := filepath.Clean(s)
	fspath := filepath.FromSlash(path)
	path16 := utf16.Encode(([]rune)(fspath))
	if len(path16) > 32767 {
		return nil, syscall.ENAMETOOLONG
	}
	return path16, nil
}

// ConvertStringSetToSlice is a helper function used to convert the contents of
// `buf` into a string slice. `buf` contains a set of null terminated strings
// with an additional null at the end to indicate the end of the set.
func ConvertStringSetToSlice(buf []byte) ([]string, error) {
	var results []string
	prev := 0
	for i := range buf {
		if buf[i] == 0 {
			if prev == i {
				// found two null characters in a row, return result
				return results, nil
			}
			results = append(results, string(buf[prev:i]))
			prev = i + 1
		}
	}
	return nil, errors.New("string set malformed: missing null terminator at end of buffer")
}
