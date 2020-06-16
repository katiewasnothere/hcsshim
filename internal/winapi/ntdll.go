package winapi

//sys NTCreateFile(handle *uintptr, accessMask uint32, oa *ObjectAttributes, iosb *IOStatusBlock, allocationSize *uint64, fileAttributes uint32, shareAccess uint32, createDisposition uint32, createOptions uint32, eaBuffer *byte, eaLength uint32) (status uint32) = ntdll.NtCreateFile
//sys NTSetInformationFile(handle uintptr, iosb *IOStatusBlock, information uintptr, length uint32, class uint32) (status uint32) = ntdll.NtSetInformationFile

//sys NTOpenDirectoryObject(handle *uintptr, accessMask uint32, oa *ObjectAttributes) (status uint32) = ntdll.NtOpenDirectoryObject
//sys NTQueryDirectoryObject(handle uintptr, buffer *byte, length uint32, singleEntry bool, restartScan bool, context *uint32, returnLength *uint32)(status uint32) = ntdll.NtQueryDirectoryObject

//sys RtlNtStatusToDosError(status uint32) (winerr error) = ntdll.RtlNtStatusToDosError
