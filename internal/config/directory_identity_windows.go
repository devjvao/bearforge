package config

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openedDirectoryID(dir *os.File, _ os.FileInfo) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(dir.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, info.CreationTime.HighDateTime, info.CreationTime.LowDateTime), nil
}
