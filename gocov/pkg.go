// Copyright (c) 2013 The Gocov Authors.
//
// Portions Copyright 2011 The Go Authors.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to
// deal in the Software without restriction, including without limitation the
// rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
// sell copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
// FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS
// IN THE SOFTWARE.

package main

// package lookup helpers, based on cmd/go/main.go

import (
	"fmt"
	"go/build"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// importPaths returns the import paths to use for the given command line.
func importPaths(args ...string) []string {
	fmt.Println("importPaths", args)
	args = importPathsNoDotExpansion(args)
	var out []string
	for _, a := range args {
		if strings.Contains(a, "...") {
			if build.IsLocalImport(a) {
				out = append(out, allPackagesInFS(a)...)
			} else {
				out = append(out, allPackages(a)...)
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// importPathsNoDotExpansion returns the import paths to use for the given
// command line, but it does no ... expansion.
func importPathsNoDotExpansion(args []string) []string {
	if len(args) == 0 {
		return []string{"."}
	}
	var out []string
	for _, a := range args {
		// Arguments are supposed to be import paths, but
		// as a courtesy to Windows developers, rewrite \ to /
		// in command-line arguments.  Handles .\... and so on.
		if filepath.Separator == '\\' {
			a = strings.Replace(a, `\`, `/`, -1)
		}

		// Put argument in canonical form, but preserve leading ./.
		if strings.HasPrefix(a, "./") {
			a = "./" + path.Clean(a)
			if a == "./." {
				a = "."
			}
		} else {
			a = path.Clean(a)
		}
		if a == "all" || a == "std" {
			out = append(out, allPackages(a)...)
			continue
		}
		out = append(out, a)
	}
	return out
}

// matchPattern(pattern)(name) reports whether
// name matches pattern.  Pattern is a limited glob
// pattern in which '...' means 'any string' and there
// is no other special syntax.
func matchPattern(pattern string) func(name string) bool {
	re := regexp.QuoteMeta(pattern)
	re = strings.Replace(re, `\.\.\.`, `.*`, -1)
	// Special case: foo/... matches foo too.
	if strings.HasSuffix(re, `/.*`) {
		re = re[:len(re)-len(`/.*`)] + `(/.*)?`
	}
	reg := regexp.MustCompile(`^` + re + `$`)
	return func(name string) bool {
		return reg.MatchString(name)
	}
}

// allPackages returns all the packages that can be found
// under the $GOPATH directories and $GOROOT matching pattern.
// The pattern is either "all" (all packages), "std" (standard packages)
// or a path including "...".
func allPackages(pattern string) []string {
	pkgs := matchPackages(pattern)
	if len(pkgs) == 0 {
		warnf("warning: %q matched no packages\n", pattern)
	}
	return pkgs
}

func matchPackages(pattern string) []string {
	match := func(string) bool { return true }
	if pattern != "all" && pattern != "std" {
		match = matchPattern(pattern)
	}

	have := map[string]bool{
		"builtin": true, // ignore pseudo-package that exists only for documentation
	}
	var pkgs []string

	// Commands
	cmd := filepath.Join(runtime.GOROOT(), "src/cmd") + string(filepath.Separator)
	filepath.Walk(cmd, func(path string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() || path == cmd {
			return nil
		}
		name := path[len(cmd):]
		// Commands are all in cmd/, not in subdirectories.
		if strings.Contains(name, string(filepath.Separator)) {
			return filepath.SkipDir
		}

		_, err = build.ImportDir(path, 0)
		if err != nil {
			return nil
		}

		// We use, e.g., cmd/gofmt as the pseudo import path for gofmt.
		name = "cmd/" + name
		if !have[name] {
			have[name] = true
			if match(name) {
				pkgs = append(pkgs, name)
			}
		}
		return nil
	})
	gorootSrcPkg := filepath.Join(runtime.GOROOT(), "src/pkg")
	for _, src := range build.Default.SrcDirs() {
		if pattern == "std" && src != gorootSrcPkg {
			continue
		}
		src = filepath.Clean(src) + string(filepath.Separator)
		filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
			if err != nil || !fi.IsDir() || path == src {
				return nil
			}

			// Avoid .foo, _foo, and testdata directory trees.
			_, elem := filepath.Split(path)
			if strings.HasPrefix(elem, ".") || strings.HasPrefix(elem, "_") || elem == "testdata" {
				return filepath.SkipDir
			}

			name := filepath.ToSlash(path[len(src):])
			if pattern == "std" && strings.Contains(name, ".") {
				return filepath.SkipDir
			}
			if have[name] {
				return nil
			}
			have[name] = true

			_, err = build.Default.ImportDir(path, 0)
			if err != nil && strings.Contains(err.Error(), "no Go source files") {
				return nil
			}
			if match(name) {
				pkgs = append(pkgs, name)
			}
			return nil
		})
	}
	return pkgs
}

// allPackagesInFS is like allPackages but is passed a pattern
// beginning ./ or ../, meaning it should scan the tree rooted
// at the given directory.  There are ... in the pattern too.
func allPackagesInFS(pattern string) []string {
	pkgs := matchPackagesInFS(pattern)
	if len(pkgs) == 0 {
		warnf("warning: %q matched no packages\n", pattern)
	}
	return pkgs
}

func matchPackagesInFS(pattern string) []string {
	// Find directory to begin the scan.
	// Could be smarter but this one optimization
	// is enough for now, since ... is usually at the
	// end of a path.
	i := strings.Index(pattern, "...")
	dir, _ := path.Split(pattern[:i])

	// pattern begins with ./ or ../.
	// path.Clean will discard the ./ but not the ../.
	// We need to preserve the ./ for pattern matching
	// and in the returned import paths.
	prefix := ""
	if strings.HasPrefix(pattern, "./") {
		prefix = "./"
	}
	match := matchPattern(pattern)

	var pkgs []string
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil
		}
		if path == dir {
			// filepath.Walk starts at dir and recurses. For the recursive case,
			// the path is the result of filepath.Join, which calls filepath.Clean.
			// The initial case is not Cleaned, though, so we do this explicitly.
			//
			// This converts a path like "./io/" to "io". Without this step, running
			// "cd $GOROOT/src/pkg; go list ./io/..." would incorrectly skip the io
			// package, because prepending the prefix "./" to the unclean path would
			// result in "././io", and match("././io") returns false.
			path = filepath.Clean(path)
		}

		// Avoid .foo, _foo, and testdata directory trees, but do not avoid "." or "..".
		_, elem := filepath.Split(path)
		dot := strings.HasPrefix(elem, ".") && elem != "." && elem != ".."
		if dot || strings.HasPrefix(elem, "_") || elem == "testdata" {
			return filepath.SkipDir
		}

		name := prefix + filepath.ToSlash(path)
		if !match(name) {
			return nil
		}
		if _, err = build.ImportDir(path, 0); err != nil {
			return nil
		}
		pkgs = append(pkgs, name)
		return nil
	})
	return pkgs
}
