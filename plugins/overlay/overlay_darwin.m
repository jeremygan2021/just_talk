//go:build darwin && cgo

#include "overlay_darwin.h"

#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#include <dispatch/dispatch.h>
#include <math.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

// Geometry mirrored from plugins/overlay/render.go so the macOS capsule
// matches the Linux one: a fixed-size transparent window with the
// capsule centred inside it, leaving room for the blue halo.
static const CGFloat kGlowPad = 14.0;
static const CGFloat kNarrowPillW = 150.0;
static const CGFloat kNarrowPillH = 42.0;
static const CGFloat kWidePillW = 240.0;
static const CGFloat kWidePillH = 70.0;
static const CGFloat kMargin = 28.0;
static const CGFloat kDotSize = 14.0;
static const CGFloat kRowGap = 14.0;
static const CGFloat kWidePadX = 18.0;
static const CGFloat kWidePadTop = 9.0;
static const CGFloat kWidePadBottom = 9.0;
static const int kBarCount = 15;
static const CGFloat kBarWidth = 4.0;
static const CGFloat kBarGap = 3.0;
static const CGFloat kBarMaxH = 26.0;
static const CGFloat kBarMinH = 3.0;

static CGFloat jt_window_width(CGFloat scale) { return (kWidePillW + 2.0 * kGlowPad) * scale; }
static CGFloat jt_window_height(CGFloat scale) { return (kWidePillH + 2.0 * kGlowPad) * scale; }

@interface JTOverlayView : NSView {
	NSString *label;
	NSColor *dotColor;
	CGFloat scale;
	BOOL wideMode;
	float *waveformLevels;
	int waveformCount;
	double phase;
}
- (void)setScaleValue:(CGFloat)newScale;
- (void)setLabel:(NSString *)newLabel red:(CGFloat)r green:(CGFloat)g blue:(CGFloat)b;
- (void)setLabel:(NSString *)newLabel red:(CGFloat)r green:(CGFloat)g blue:(CGFloat)b levels:(const float *)levels count:(int)n phase:(double)newPhase;
@end

@implementation JTOverlayView
- (instancetype)initWithFrame:(NSRect)frame {
	self = [super initWithFrame:frame];
	if (self) {
		label = [@"IDLE" retain];
		dotColor = [[NSColor colorWithCalibratedRed:0.57 green:0.57 blue:0.57 alpha:1.0] retain];
		scale = 1.0;
		wideMode = NO;
		waveformLevels = NULL;
		waveformCount = 0;
		phase = 0.0;
	}
	return self;
}
- (void)dealloc {
	[label release];
	[dotColor release];
	if (waveformLevels != NULL) free(waveformLevels);
	[super dealloc];
}
- (BOOL)isOpaque { return NO; }
- (void)setScaleValue:(CGFloat)newScale {
	scale = newScale <= 0 ? 1.0 : newScale;
	[self setNeedsDisplay:YES];
}
- (void)setLabel:(NSString *)newLabel red:(CGFloat)r green:(CGFloat)g blue:(CGFloat)b {
	[self setLabel:newLabel red:r green:g blue:b levels:NULL count:0 phase:0.0];
}
- (void)setLabel:(NSString *)newLabel red:(CGFloat)r green:(CGFloat)g blue:(CGFloat)b levels:(const float *)levels count:(int)n phase:(double)newPhase {
	[label release];
	label = [newLabel retain];
	[dotColor release];
	dotColor = [[NSColor colorWithCalibratedRed:r green:g blue:b alpha:1.0] retain];
	if (waveformLevels != NULL) {
		free(waveformLevels);
		waveformLevels = NULL;
		waveformCount = 0;
	}
	wideMode = (n > 0);
	if (wideMode) {
		waveformLevels = (float *)malloc(sizeof(float) * (size_t)n);
		if (waveformLevels != NULL) {
			memcpy(waveformLevels, levels, sizeof(float) * (size_t)n);
			waveformCount = n;
		} else {
			waveformCount = 0;
			wideMode = NO;
		}
	}
	phase = newPhase;
	[self setNeedsDisplay:YES];
}
- (void)drawRect:(NSRect)dirtyRect {
	(void)dirtyRect;
	NSRect bounds = [self bounds];

	CGFloat pillW = (wideMode ? kWidePillW : kNarrowPillW) * scale;
	CGFloat pillH = (wideMode ? kWidePillH : kNarrowPillH) * scale;
	NSRect pillRect = NSMakeRect((NSWidth(bounds) - pillW) / 2.0, (NSHeight(bounds) - pillH) / 2.0, pillW, pillH);
	NSBezierPath *pill = [NSBezierPath bezierPathWithRoundedRect:pillRect
		xRadius:pillH / 2.0 yRadius:pillH / 2.0];

	// Peak level drives the glow and the indicator halo, exactly like
	// the Linux renderer.
	CGFloat level = 0.0;
	for (int i = 0; i < waveformCount; i++) {
		if (waveformLevels[i] > level) level = waveformLevels[i];
	}
	CGFloat breath = 0.5 + 0.5 * sin(2.0 * M_PI * phase / 2.8);
	CGFloat glow = 0.34 + 0.16 * breath;
	if (wideMode) glow += 0.62 * level;
	if (glow > 1.0) glow = 1.0;

	// Soft blue halo around the capsule (replaces the native window
	// shadow, which would draw a dark ring around the glow).
	NSShadow *halo = [[NSShadow alloc] init];
	[halo setShadowColor:[NSColor colorWithCalibratedRed:0.28 green:0.59 blue:1.0 alpha:0.55 * glow]];
	[halo setShadowBlurRadius:11.0 * scale];
	[halo setShadowOffset:NSMakeSize(0.0, 0.0)];
	[NSGraphicsContext saveGraphicsState];
	[halo set];
	NSGradient *background = [[NSGradient alloc] initWithStartingColor:[NSColor colorWithCalibratedRed:0.118 green:0.133 blue:0.188 alpha:0.94]
		endingColor:[NSColor colorWithCalibratedRed:0.043 green:0.051 blue:0.078 alpha:0.96]];
	[background drawInBezierPath:pill angle:-90.0];
	[background release];
	[NSGraphicsContext restoreGraphicsState];
	[halo release];

	// Blue rim light, brighter while the user speaks.
	CGFloat rimStrength = 0.55 + 0.35 * level + 0.12 * breath;
	if (rimStrength > 1.0) rimStrength = 1.0;
	[[NSColor colorWithCalibratedRed:0.52 green:0.75 blue:1.0 alpha:0.62 * rimStrength] setStroke];
	[pill setLineWidth:MAX(1.0, scale)];
	[pill stroke];

	NSDictionary *attrs = @{
		NSFontAttributeName: [NSFont boldSystemFontOfSize:13.0 * scale],
		NSForegroundColorAttributeName: [NSColor colorWithCalibratedRed:0.93 green:0.96 blue:1.0 alpha:1.0],
	};
	NSSize textSize = [label sizeWithAttributes:attrs];
	CGFloat dotSize = kDotSize * scale;
	CGFloat gap = kRowGap * scale;
	CGFloat textW = textSize.width;

	CGFloat dotCenterY;
	CGFloat textY;
	if (wideMode) {
		// Row 1: dot + label, centred. Row 2: the live waveform.
		CGFloat padTop = kWidePadTop * scale;
		CGFloat rowH = MAX(dotSize, textSize.height);
		CGFloat rowTop = NSMaxY(pillRect) - padTop - rowH;
		CGFloat contentW = dotSize + gap + textW;
		CGFloat rowX = NSMinX(pillRect) + (pillW - contentW) / 2.0;
		if (rowX < NSMinX(pillRect) + kWidePadX * scale) rowX = NSMinX(pillRect) + kWidePadX * scale;

		dotCenterY = rowTop + rowH / 2.0;
		textY = rowTop + (rowH - textSize.height) / 2.0;

		NSRect haloRect = NSMakeRect(rowX + dotSize / 2.0 - dotSize * (0.75 + 0.6 * level),
			dotCenterY - dotSize * (0.75 + 0.6 * level),
			dotSize * (1.5 + 1.2 * level), dotSize * (1.5 + 1.2 * level));
		NSBezierPath *dotHalo = [NSBezierPath bezierPathWithOvalInRect:haloRect];
		[[dotColor colorWithAlphaComponent:0.20 + 0.33 * level] setFill];
		[dotHalo fill];

		NSBezierPath *dot = [NSBezierPath bezierPathWithOvalInRect:
			NSMakeRect(rowX, dotCenterY - dotSize / 2.0, dotSize, dotSize)];
		[dotColor setFill];
		[dot fill];

		[label drawAtPoint:NSMakePoint(rowX + dotSize + gap, textY) withAttributes:attrs];

		CGFloat barW = kBarWidth * scale;
		CGFloat barGap = kBarGap * scale;
		CGFloat barMaxH = kBarMaxH * scale;
		CGFloat barMinH = kBarMinH * scale;
		CGFloat barsW = (CGFloat)kBarCount * barW + (CGFloat)(kBarCount - 1) * barGap;
		CGFloat barsX = NSMinX(pillRect) + (pillW - barsW) / 2.0;
		CGFloat centerY = NSMinY(pillRect) + kWidePadBottom * scale + barMaxH / 2.0;
		for (int i = 0; i < kBarCount; i++) {
			CGFloat v = (i < waveformCount) ? waveformLevels[i] : 0.0;
			if (v < 0.0) v = 0.0;
			if (v > 1.0) v = 1.0;
			CGFloat h = barMinH + (barMaxH - barMinH) * v;
			CGFloat bx = barsX + (CGFloat)i * (barW + barGap);
			NSRect barRect = NSMakeRect(bx, centerY - h / 2.0, barW, h);

			// Neon fringe behind the bar.
			NSRect glowRect = NSInsetRect(barRect, -1.5 * scale, -1.5 * scale);
			NSBezierPath *barGlow = [NSBezierPath bezierPathWithRoundedRect:glowRect
				xRadius:glowRect.size.width / 2.0 yRadius:glowRect.size.width / 2.0];
			[[NSColor colorWithCalibratedRed:0.31 green:0.75 blue:1.0 alpha:0.10 + 0.38 * v] setFill];
			[barGlow fill];

			NSBezierPath *bar = [NSBezierPath bezierPathWithRoundedRect:barRect
				xRadius:barW / 2.0 yRadius:barW / 2.0];
			[[NSColor colorWithCalibratedRed:0.45 + 0.15 * v green:0.72 + 0.20 * v blue:1.0 alpha:1.0] setFill];
			[bar fill];
		}
	} else {
		CGFloat contentW = dotSize + gap + textW;
		CGFloat contentX = NSMinX(pillRect) + (pillW - contentW) / 2.0;
		if (contentX < NSMinX(pillRect) + 8.0 * scale) contentX = NSMinX(pillRect) + 8.0 * scale;
		dotCenterY = NSMidY(pillRect);
		textY = dotCenterY - textSize.height / 2.0;

		NSRect haloRect = NSMakeRect(contentX + dotSize / 2.0 - dotSize * (0.75 + 0.6 * level),
			dotCenterY - dotSize * (0.75 + 0.6 * level),
			dotSize * (1.5 + 1.2 * level), dotSize * (1.5 + 1.2 * level));
		NSBezierPath *dotHalo = [NSBezierPath bezierPathWithOvalInRect:haloRect];
		[[dotColor colorWithAlphaComponent:0.20 + 0.33 * level] setFill];
		[dotHalo fill];

		NSBezierPath *dot = [NSBezierPath bezierPathWithOvalInRect:
			NSMakeRect(contentX, dotCenterY - dotSize / 2.0, dotSize, dotSize)];
		[dotColor setFill];
		[dot fill];

		[label drawAtPoint:NSMakePoint(contentX + dotSize + gap, textY) withAttributes:attrs];
	}
}
@end

typedef struct {
	NSPanel *panel;
	JTOverlayView *view;
	char position[32];
	CGFloat scale;
} jt_overlay_t;

static jt_overlay_t *helper_overlay = NULL;

static void jt_overlay_on_main_sync(void (^block)(void)) {
	if (pthread_main_np()) {
		block();
		return;
	}
	dispatch_sync(dispatch_get_main_queue(), block);
}

static void jt_overlay_pump(void) {
	NSEvent *event = nil;
	do {
		event = [NSApp nextEventMatchingMask:NSEventMaskAny
			untilDate:[NSDate distantPast]
			inMode:NSDefaultRunLoopMode
			dequeue:YES];
		if (event != nil) {
			[NSApp sendEvent:event];
		}
	} while (event != nil);
}

static void jt_overlay_move(jt_overlay_t *overlay) {
	NSScreen *screen = [NSScreen mainScreen];
	if (screen == nil) return;
	NSRect frame = [screen visibleFrame];
	CGFloat w = jt_window_width(overlay->scale);
	CGFloat h = jt_window_height(overlay->scale);
	// The window is fixed size and anchored so that the *capsule* keeps
	// kMargin from the screen edge; the transparent glow padding sits
	// outside that margin.
	CGFloat margin = (kMargin - kGlowPad) * overlay->scale;
	if (margin < 0.0) margin = 0.0;
	CGFloat x = NSMaxX(frame) - w - margin;
	CGFloat y = NSMaxY(frame) - h - margin;

	if (strcmp(overlay->position, "top-left") == 0) {
		x = NSMinX(frame) + margin; y = NSMaxY(frame) - h - margin;
	} else if (strcmp(overlay->position, "top-center") == 0) {
		x = NSMinX(frame) + (NSWidth(frame) - w) / 2.0; y = NSMaxY(frame) - h - margin;
	} else if (strcmp(overlay->position, "bottom-left") == 0) {
		x = NSMinX(frame) + margin; y = NSMinY(frame) + margin;
	} else if (strcmp(overlay->position, "bottom-center") == 0) {
		x = NSMinX(frame) + (NSWidth(frame) - w) / 2.0; y = NSMinY(frame) + margin;
	} else if (strcmp(overlay->position, "bottom-right") == 0) {
		x = NSMaxX(frame) - w - margin; y = NSMinY(frame) + margin;
	}
	[overlay->panel setFrame:NSMakeRect(x, y, w, h) display:YES];
}

void *jt_overlay_create(const char *position, double scale) {
	__block jt_overlay_t *overlay = NULL;
	double scaleCopy = scale;
	if (position == NULL || position[0] == '\0') position = "bottom-center";
	char *positionCopy = strdup(position);
	jt_overlay_on_main_sync(^{
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		[NSApp finishLaunching];

		overlay = calloc(1, sizeof(jt_overlay_t));
		if (overlay == NULL) return;
		overlay->scale = scaleCopy <= 0 ? 1.0 : scaleCopy;
		snprintf(overlay->position, sizeof(overlay->position), "%s", positionCopy == NULL ? "top-right" : positionCopy);

		CGFloat w = jt_window_width(overlay->scale);
		CGFloat h = jt_window_height(overlay->scale);
		overlay->panel = [[NSPanel alloc] initWithContentRect:NSMakeRect(0, 0, w, h)
			styleMask:NSWindowStyleMaskBorderless
			backing:NSBackingStoreBuffered
			defer:NO];
		[overlay->panel setOpaque:NO];
		[overlay->panel setBackgroundColor:[NSColor clearColor]];
		// The blue halo is drawn by the view; the native window shadow
		// would add a dark ring around it.
		[overlay->panel setHasShadow:NO];
		[overlay->panel setIgnoresMouseEvents:YES];
		[overlay->panel setCanHide:NO];
		[overlay->panel setHidesOnDeactivate:NO];
		[overlay->panel setReleasedWhenClosed:NO];
		[overlay->panel setLevel:NSFloatingWindowLevel];
		[overlay->panel setAlphaValue:1.0];
		[overlay->panel setCollectionBehavior:
			NSWindowCollectionBehaviorCanJoinAllSpaces |
			NSWindowCollectionBehaviorStationary |
			NSWindowCollectionBehaviorFullScreenAuxiliary];

		overlay->view = [[JTOverlayView alloc] initWithFrame:NSMakeRect(0, 0, w, h)];
		[overlay->view setScaleValue:overlay->scale];
		[overlay->panel setContentView:overlay->view];
		jt_overlay_move(overlay);
		[overlay->view display];
		[overlay->panel display];
		jt_overlay_pump();
	});
	if (positionCopy != NULL) free(positionCopy);
	return overlay;
}

void jt_overlay_show(void *handle, const char *labelText, unsigned short r, unsigned short g, unsigned short b) {
	jt_overlay_show_wide(handle, labelText, r, g, b, NULL, 0, 0.0);
}

void jt_overlay_show_wide(void *handle, const char *labelText, unsigned short r, unsigned short g, unsigned short b, const float *levels, int n, double phase) {
	jt_overlay_t *overlay = (jt_overlay_t *)handle;
	if (overlay == NULL) return;
	char *labelCopy = strdup(labelText == NULL ? "" : labelText);
	float *levelsCopy = NULL;
	if (levels != NULL && n > 0) {
		levelsCopy = (float *)malloc(sizeof(float) * (size_t)n);
		if (levelsCopy != NULL) {
			memcpy(levelsCopy, levels, sizeof(float) * (size_t)n);
		}
	}
	jt_overlay_on_main_sync(^{
		NSString *text = [NSString stringWithUTF8String:labelCopy == NULL ? "" : labelCopy];
		[overlay->view setLabel:text
							red:((CGFloat)r / 65535.0)
						  green:((CGFloat)g / 65535.0)
						   blue:((CGFloat)b / 65535.0)
						 levels:(levelsCopy != NULL ? levelsCopy : NULL)
						   count:(levelsCopy != NULL ? n : 0)
						   phase:phase];
		[overlay->panel setIsVisible:YES];
		[overlay->panel setAlphaValue:1.0];
		[overlay->panel orderFrontRegardless];
		[overlay->view display];
		[overlay->panel display];
		jt_overlay_pump();
	});
	if (labelCopy != NULL) free(labelCopy);
	if (levelsCopy != NULL) free(levelsCopy);
}

void jt_overlay_hide(void *handle) {
	jt_overlay_t *overlay = (jt_overlay_t *)handle;
	if (overlay == NULL) return;
	jt_overlay_on_main_sync(^{
		[overlay->panel orderOut:nil];
		jt_overlay_pump();
	});
}

void jt_overlay_close(void *handle) {
	jt_overlay_t *overlay = (jt_overlay_t *)handle;
	if (overlay == NULL) return;
	jt_overlay_on_main_sync(^{
		[overlay->panel orderOut:nil];
		[overlay->view release];
		[overlay->panel close];
		[overlay->panel release];
		free(overlay);
		jt_overlay_pump();
	});
}

void jt_overlay_helper_init(const char *position, double scale) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	[NSApp finishLaunching];
	helper_overlay = (jt_overlay_t *)jt_overlay_create(position, scale);
}

void jt_overlay_helper_run_app(void) {
	[NSApp run];
}

void jt_overlay_run_helper(const char *position, double scale) {
	jt_overlay_helper_init(position, scale);
	jt_overlay_helper_run_app();
}

void jt_overlay_helper_show(const char *label, unsigned short r, unsigned short g, unsigned short b) {
	if (helper_overlay == NULL) return;
	jt_overlay_show(helper_overlay, label, r, g, b);
}

void jt_overlay_helper_show_wide(const char *label, unsigned short r, unsigned short g, unsigned short b, const float *levels, int n, double phase) {
	if (helper_overlay == NULL) return;
	jt_overlay_show_wide(helper_overlay, label, r, g, b, levels, n, phase);
}

void jt_overlay_helper_hide(void) {
	if (helper_overlay == NULL) return;
	jt_overlay_hide(helper_overlay);
}

void jt_overlay_helper_close(void) {
	if (helper_overlay != NULL) {
		jt_overlay_close(helper_overlay);
		helper_overlay = NULL;
	}
	[NSApp terminate:nil];
}
