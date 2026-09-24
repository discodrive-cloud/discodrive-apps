//go:build darwin && cgo

// Package davcredentials keeps reusable DAV passwords in the system credential store.
package davcredentials

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
static CFMutableDictionaryRef davQuery(const char *service) {
 CFMutableDictionaryRef q=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFStringRef s=CFStringCreateWithCString(NULL,service,kCFStringEncodingUTF8);
 CFDictionarySetValue(q,kSecClass,kSecClassGenericPassword);
 CFDictionarySetValue(q,kSecAttrService,s); CFRelease(s);
 return q;
}
static int davRead(const char *service, char **value) {
 CFMutableDictionaryRef q=davQuery(service);
 CFDictionarySetValue(q,kSecReturnData,kCFBooleanTrue);
 CFTypeRef data=NULL; OSStatus status=SecItemCopyMatching(q,&data); CFRelease(q);
 if(status!=errSecSuccess) return status;
 CFIndex size=CFDataGetLength((CFDataRef)data);
 *value=malloc(size+1);
 if(!*value) {CFRelease(data); return errSecAllocate;}
 memcpy(*value,CFDataGetBytePtr((CFDataRef)data),size); (*value)[size]=0; CFRelease(data); return 0;
}
static int davWrite(const char *service, const char *value) {
 CFMutableDictionaryRef q=davQuery(service);
 CFDataRef data=CFDataCreate(NULL,(const UInt8*)value,strlen(value));
 CFMutableDictionaryRef attrs=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFDictionarySetValue(attrs,kSecValueData,data);
 OSStatus status=SecItemUpdate(q,attrs);
 if(status==errSecItemNotFound) {CFDictionarySetValue(q,kSecValueData,data); status=SecItemAdd(q,NULL);}
 CFRelease(attrs); CFRelease(data); CFRelease(q); return status;
}
static int davDelete(const char *service) {CFMutableDictionaryRef q=davQuery(service); OSStatus status=SecItemDelete(q); CFRelease(q); return status==errSecItemNotFound ? 0 : status;}
*/
import "C"
import (
	"fmt"
	"unsafe"
)

func Load(service string) (string, error) {
	s := C.CString(service)
	defer C.free(unsafe.Pointer(s))
	var out *C.char
	status := C.davRead(s, &out)
	if status == C.errSecItemNotFound {
		return "", nil
	}
	if status != 0 {
		return "", fmt.Errorf("keychain read: %d", status)
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out), nil
}
func Save(service, value string) error {
	s, v := C.CString(service), C.CString(value)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(v))
	if status := C.davWrite(s, v); status != 0 {
		return fmt.Errorf("keychain write: %d", status)
	}
	return nil
}
func Delete(service string) error {
	s := C.CString(service)
	defer C.free(unsafe.Pointer(s))
	if status := C.davDelete(s); status != 0 {
		return fmt.Errorf("keychain delete: %d", status)
	}
	return nil
}
