//go:build darwin

// Objective-C clipboard access backed by NSPasteboard. Compiled by cgo and
// linked with AppKit.

#import <AppKit/AppKit.h>
#import <stdlib.h>
#import <string.h>

int cs_pb_write_text(const char *utf8) {
	@autoreleasepool {
		NSString *s = [NSString stringWithUTF8String:utf8];
		if (!s) return -1;
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		return [pb setString:s forType:NSPasteboardTypeString] ? 0 : -1;
	}
}

char *cs_pb_read_text(void) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		NSString *s = [pb stringForType:NSPasteboardTypeString];
		if (!s) return NULL;
		const char *u = [s UTF8String];
		if (!u) return NULL;
		size_t n = strlen(u);
		char *out = (char *)malloc(n + 1);
		if (out) memcpy(out, u, n + 1);
		return out;
	}
}

// cs_pb_read_files_json returns a malloc'd UTF-8 JSON array of
// {"name","size","path"} for the regular files currently referenced by the
// pasteboard, or NULL when no file references exist.
char *cs_pb_read_files_json(void) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		NSArray *urls = [pb readObjectsForClasses:@[[NSURL class]]
			options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
		if (!urls || urls.count == 0) return NULL;

		NSMutableArray *items = [NSMutableArray arrayWithCapacity:urls.count];
		NSFileManager *fm = [NSFileManager defaultManager];
		for (NSURL *u in urls) {
			NSString *path = u.path;
			if (!path) continue;
			BOOL isDir = NO;
			if (![fm fileExistsAtPath:path isDirectory:&isDir] || isDir) continue;
			long long size = 0;
			NSDictionary *attr = [fm attributesOfItemAtPath:path error:nil];
			if (attr) size = (long long)[attr fileSize];
			[items addObject:@{
				@"name": path.lastPathComponent ?: @"",
				@"size": @(size),
				@"path": path,
			}];
		}
		if (items.count == 0) return NULL;

		NSError *err = nil;
		NSData *data = [NSJSONSerialization dataWithJSONObject:items options:0 error:&err];
		if (!data || data.length == 0) return NULL;
		char *out = (char *)malloc(data.length + 1);
		if (!out) return NULL;
		memcpy(out, data.bytes, data.length);
		out[data.length] = 0;
		return out;
	}
}

// cs_pb_write_files_json takes a JSON object {"files":[{"path":...},...]} and
// writes the referenced files to the pasteboard as file URL objects (Finder
// and other apps then paste real files).
int cs_pb_write_files_json(const char *json) {
	@autoreleasepool {
		if (!json) return -1;
		NSData *data = [NSData dataWithBytes:json length:strlen(json)];
		id parsed = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
		if (![parsed isKindOfClass:[NSDictionary class]]) return -1;
		NSArray *in = parsed[@"files"];
		if (![in isKindOfClass:[NSArray class]] || in.count == 0) return -1;

		NSMutableArray *urls = [NSMutableArray arrayWithCapacity:in.count];
		for (id item in in) {
			if (![item isKindOfClass:[NSDictionary class]]) continue;
			NSString *path = item[@"path"];
			if ([path isKindOfClass:[NSString class]] && path.length > 0) {
				[urls addObject:[NSURL fileURLWithPath:path]];
			}
		}
		if (urls.count == 0) return -1;
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		return [pb writeObjects:urls] ? 0 : -1;
	}
}

long long cs_pb_change_count(void) {
	@autoreleasepool {
		return (long long)[[NSPasteboard generalPasteboard] changeCount];
	}
}
