package fullsync

import (
	"fmt"
	"golang.org/x/sys/windows"
)

func identity(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var st windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &st); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", st.VolumeSerialNumber, uint64(st.FileIndexHigh)<<32|uint64(st.FileIndexLow)), nil
}
