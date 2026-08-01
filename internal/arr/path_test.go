package arr

import "testing"

func TestRemoteMediaPathMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		media         string
		mapping       *PathMapping
		localWindows  bool
		remoteWindows bool
		want          string
		wantErr       bool
	}{
		{name: "no mapping", media: "/media/TV/Show/Episode.mkv", want: "/media/TV/Show/Episode.mkv"},
		{name: "unix mapping", media: "/Volumes/Media/TV/Show/Episode.mkv", mapping: &PathMapping{LocalPrefix: "/Volumes/Media/TV", RemotePrefix: "/tv"}, want: "/tv/Show/Episode.mkv"},
		{name: "clean mapping", media: "/Volumes/Media/TV/Show/../Show/Episode.mkv", mapping: &PathMapping{LocalPrefix: "/Volumes/Media/TV/", RemotePrefix: "/tv/"}, want: "/tv/Show/Episode.mkv"},
		{name: "boundary mismatch", media: "/Volumes/Media/TV2/Show/Episode.mkv", mapping: &PathMapping{LocalPrefix: "/Volumes/Media/TV", RemotePrefix: "/tv"}, want: "/Volumes/Media/TV2/Show/Episode.mkv"},
		{name: "windows mapping is case insensitive", media: `C:\Media\TV\Show\Episode.mkv`, mapping: &PathMapping{LocalPrefix: `c:\media\tv`, RemotePrefix: `D:\Series`}, localWindows: true, remoteWindows: true, want: "D:/Series/Show/Episode.mkv"},
		{name: "VLC Windows file URI path", media: `/C:/Media/TV/Show/Episode.mkv`, mapping: &PathMapping{LocalPrefix: `C:\Media\TV`, RemotePrefix: `D:\Series`}, localWindows: true, remoteWindows: true, want: "D:/Series/Show/Episode.mkv"},
		{name: "POSIX C-colon mapping stays case sensitive", media: `/C:/Media/TV/Show/Episode.mkv`, mapping: &PathMapping{LocalPrefix: `/c:/Media/TV`, RemotePrefix: `/mapped`}, want: "/C:/Media/TV/Show/Episode.mkv"},
		{name: "partial mapping", media: "/media/file.mkv", mapping: &PathMapping{LocalPrefix: "/media"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := remoteMediaPath(test.media, test.mapping, test.localWindows, test.remoteWindows)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("path = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeRemotePathUsesServerOS(t *testing.T) {
	t.Parallel()
	if got := normalizeRemotePath(`C:\MEDIA\Show\Episode.mkv`, true); got != "c:/media/show/episode.mkv" {
		t.Fatalf("Windows path = %q", got)
	}
	if got := normalizeRemotePath(`/C:/MEDIA/Show/Episode.mkv`, true); got != "c:/media/show/episode.mkv" {
		t.Fatalf("VLC Windows path = %q", got)
	}
	if got := normalizeRemotePath(`/C:/MEDIA/Show/Episode.mkv`, false); got != "/C:/MEDIA/Show/Episode.mkv" {
		t.Fatalf("POSIX C-colon path = %q", got)
	}
	if got := normalizeRemotePath(`/Media/Show\Episode.mkv`, false); got != `/Media/Show\Episode.mkv` {
		t.Fatalf("POSIX backslash path = %q", got)
	}
	if got := normalizeRemotePath("/Media/Show/Episode.mkv", false); got != "/Media/Show/Episode.mkv" {
		t.Fatalf("Unix path = %q", got)
	}
	if sameOrDescendant("/tv/Showcase/file.mkv", "/tv/Show") {
		t.Fatal("path-prefix comparison ignored a segment boundary")
	}
	if !sameOrDescendant("/tv/Show/file.mkv", "/tv/Show") {
		t.Fatal("descendant path was not recognized")
	}
}

func TestRemoteMediaPathUnicodeCaseFoldDoesNotUseByteOffsets(t *testing.T) {
	t.Parallel()
	mapping := &PathMapping{LocalPrefix: `C:\İzle`, RemotePrefix: `D:\Media`}
	got, err := remoteMediaPath(`c:\izle\Show\Episode.mkv`, mapping, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Go's Unicode EqualFold deliberately does not equate dotted capital I
	// with ASCII i. The important safety property is a clean non-match rather
	// than byte slicing based on a case-converted string.
	if got != `C:/izle/Show/Episode.mkv` {
		t.Fatalf("path = %q", got)
	}
}
