//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// fyne/systray v1.12.2's C entry point. Its public SetTemplateIcon always resizes
// images to 16 pt. Send our native 18 pt asset to the same main-thread setter,
// retaining systray's existing status item, menu and lifecycle.
extern void runInMainThread(SEL method, id object);

static void setNativeTrayIcon(const void *fallback, int length) {
    @autoreleasepool {
        NSImage *image = [[NSImage imageNamed:@"TrayLogo"] copy];
        if (!image) {
            image = [[NSImage alloc] initWithData:[NSData dataWithBytes:fallback length:length]];
        }
        [image setSize:NSMakeSize(18, 18)];
        image.template = YES;
        runInMainThread(@selector(setIcon:), image);
        [image release];
    }
}
*/
import "C"

import (
	_ "embed"
	"unsafe"
)

// Fallback for unpackaged development runs; packaged apps use the shared asset catalog.
//
//go:embed tray-logo.png
var trayTemplate []byte

func setTrayIcon() { C.setNativeTrayIcon(unsafe.Pointer(&trayTemplate[0]), C.int(len(trayTemplate))) }
