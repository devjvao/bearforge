//go:build darwin && cgo

package auth

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef query(const char *key) {
 CFMutableDictionaryRef q=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFStringRef account=CFStringCreateWithCString(NULL,key,kCFStringEncodingUTF8);
 CFDictionarySetValue(q,kSecClass,kSecClassGenericPassword);
 CFDictionarySetValue(q,kSecAttrService,CFSTR("bearforge"));
 CFDictionarySetValue(q,kSecAttrAccount,account);
 CFDictionarySetValue(q,kSecUseAuthenticationUI,kSecUseAuthenticationUIFail);
 CFRelease(account); return q;
}
static int readSecret(const char *key,void **out,long *len) {
 CFMutableDictionaryRef q=query(key); CFDictionarySetValue(q,kSecReturnData,kCFBooleanTrue);
 CFTypeRef result=NULL; OSStatus status=SecItemCopyMatching(q,&result); CFRelease(q);
 if(status==errSecSuccess){CFDataRef d=(CFDataRef)result;*len=CFDataGetLength(d);*out=malloc(*len);memcpy(*out,CFDataGetBytePtr(d),*len);CFRelease(result);}return status;
}
static int writeSecret(const char *key,const void *value,long len) {
 CFMutableDictionaryRef q=query(key); CFDataRef data=CFDataCreate(NULL,value,len);
 CFDictionarySetValue(q,kSecValueData,data);OSStatus status=SecItemAdd(q,NULL);CFRelease(data);CFRelease(q);return status;
}
static int deleteSecret(const char *key){CFMutableDictionaryRef q=query(key);OSStatus status=SecItemDelete(q);CFRelease(q);return status;}
*/
import "C"
import (
	"context"
	"unsafe"
)

type systemSecretStore struct{}

func NewSystemSecretStore() SecretStore { return systemSecretStore{} }
func (systemSecretStore) Get(ctx context.Context, key string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	k := C.CString(key)
	defer C.free(unsafe.Pointer(k))
	var data unsafe.Pointer
	var size C.long
	status := C.readSecret(k, &data, &size)
	if status == C.errSecItemNotFound {
		return "", ErrSecretMissing
	}
	if status != C.errSecSuccess {
		return "", ErrSecureStore
	}
	defer C.free(data)
	return string(C.GoBytes(data, C.int(size))), ctx.Err()
}
func (systemSecretStore) Set(ctx context.Context, key, value string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	k := C.CString(key)
	defer C.free(unsafe.Pointer(k))
	v := C.CBytes([]byte(value))
	defer C.free(v)
	if C.writeSecret(k, v, C.long(len(value))) != C.errSecSuccess {
		return ErrSecureStore
	}
	return nil
}
func (systemSecretStore) Delete(ctx context.Context, key string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	k := C.CString(key)
	defer C.free(unsafe.Pointer(k))
	status := C.deleteSecret(k)
	if status != C.errSecSuccess && status != C.errSecItemNotFound {
		return ErrSecureStore
	}
	return nil
}
