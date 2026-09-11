//go:build darwin
// +build darwin

#import <AppKit/AppKit.h>
#import <stdlib.h>

static NSPasteboard *pb;
static int lastCount;

void *jt_pb_init(int *count) {
    pb = [NSPasteboard generalPasteboard];
    lastCount = (int)[pb changeCount];
    *count = lastCount;
    return (void *)pb;
}

int jt_pb_poll_change(int *outLen, char **outText) {
    if (!pb) pb = [NSPasteboard generalPasteboard];
    int cur = (int)[pb changeCount];
    if (cur == lastCount) return 0;
    lastCount = cur;
    NSArray *types = [pb types];
    if (![types containsObject:NSPasteboardTypeString] && ![types containsObject:@"public.utf8-plain-text"]) {
        return 1; // changed but empty
    }
    NSString *s = [pb stringForType:NSPasteboardTypeString];
    if (s == nil) s = [pb stringForType:@"public.utf8-plain-text"];
    if (s == nil) return 1;
    const char *cstr = [s UTF8String];
    if (!cstr) return 1;
    size_t len = strlen(cstr);
    char *dup = (char *)malloc(len + 1);
    memcpy(dup, cstr, len + 1);
    *outText = dup;
    *outLen = (int)len;
    return 1;
}

void jt_pb_free(char *p) {
    if (p) free(p);
}
