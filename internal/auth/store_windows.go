package auth

import "golang.org/x/sys/windows"

func replaceFile(source, target string) (bool, error) {
	from, e := windows.UTF16PtrFromString(source)
	if e != nil {
		return false, e
	}
	to, e := windows.UTF16PtrFromString(target)
	if e != nil {
		return false, e
	}
	e = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	return e == nil, e
}
