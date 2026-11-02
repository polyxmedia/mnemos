package main

import (
	"os"
	"path/filepath"
	"strings"
)

// projectFromCWD names the project a working directory belongs to: the git
// repo it sits in, else the directory itself. Sessions started in a subfolder
// or a worktree used to file memory under that folder's name, which split one
// repo across several projects.
func projectFromCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return filepath.Base(repoRoot(cwd, home))
}

// repoRoot walks up from dir to the root of its git repo and returns dir
// unchanged when there is none. A linked worktree resolves to the main
// checkout, because it is the same project. It reads the filesystem instead
// of calling git so hooks stay fast and work where git is missing.
//
// The walk stops at home, so a dotfiles repo in the home directory does not
// make every folder under home one project.
func repoRoot(dir, home string) string {
	dir = filepath.Clean(dir)
	if home != "" {
		home = filepath.Clean(home)
	}
	for cur := dir; ; cur = filepath.Dir(cur) {
		if cur == home || filepath.Dir(cur) == cur {
			return dir
		}
		gitPath := filepath.Join(cur, ".git")
		info, err := os.Lstat(gitPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return cur
		}
		if main := worktreeMainRoot(gitPath); main != "" {
			return main
		}
		return cur // a submodule is its own project
	}
}

// worktreeMainRoot reads a .git file and returns the main checkout when the
// file belongs to a linked worktree, whose gitdir has the shape
// <main>/.git/worktrees/<name>. Anything else, a submodule included, returns
// "".
func worktreeMainRoot(gitFile string) string {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(gitFile), target)
	}
	worktrees := filepath.Dir(filepath.Clean(target))
	gitDir := filepath.Dir(worktrees)
	if filepath.Base(worktrees) != "worktrees" || filepath.Base(gitDir) != ".git" {
		return ""
	}
	return filepath.Dir(gitDir)
}
