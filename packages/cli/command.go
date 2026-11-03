package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
)

// Just enough of a cli framework over the standard library's flag package for
// the commands here. Every run parses into a fresh FlagSet, so connect can run
// the same command once per stdin line without one line's flags leaking into
// the next.

type Command struct {
	Name        string
	Usage       string
	Flags       []Flag
	Subcommands []*Command
	Action      func(*Context) error
}

type Flag interface {
	register(fs *flag.FlagSet)
	check(set map[string]bool) error
}

// StringFlag falls back to the first of EnvVars that is set, then to Value. A
// required flag is satisfied by its env var.
type StringFlag struct {
	Name     string
	Usage    string
	Value    string
	Required bool
	EnvVars  []string
}

type BoolFlag struct {
	Name  string
	Usage string
}

type IntFlag struct {
	Name     string
	Usage    string
	Required bool
}

type Int64Flag = IntFlag

type Uint64Flag IntFlag

func (f *StringFlag) register(fs *flag.FlagSet) {
	value := f.Value
	if env, ok := lookupEnv(f.EnvVars); ok {
		value = env
	}
	fs.String(f.Name, value, f.Usage)
}

func (f *StringFlag) check(set map[string]bool) error {
	_, fromEnv := lookupEnv(f.EnvVars)
	return required(f.Name, f.Required && !fromEnv, set)
}

func (f *BoolFlag) register(fs *flag.FlagSet)   { fs.Bool(f.Name, false, f.Usage) }
func (f *BoolFlag) check(map[string]bool) error { return nil }

func (f *IntFlag) register(fs *flag.FlagSet)       { fs.Int64(f.Name, 0, f.Usage) }
func (f *IntFlag) check(set map[string]bool) error { return required(f.Name, f.Required, set) }

func (f *Uint64Flag) register(fs *flag.FlagSet)       { fs.Uint64(f.Name, 0, f.Usage) }
func (f *Uint64Flag) check(set map[string]bool) error { return required(f.Name, f.Required, set) }

func lookupEnv(names []string) (string, bool) {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value, true
		}
	}
	return "", false
}

func required(name string, want bool, set map[string]bool) error {
	if want && !set[name] {
		return fmt.Errorf("Required flag %q not set", name)
	}
	return nil
}

type Context struct {
	Context context.Context
	fs      *flag.FlagSet
	set     map[string]bool
}

func (c *Context) String(name string) string { return c.fs.Lookup(name).Value.String() }
func (c *Context) Bool(name string) bool     { return c.String(name) == "true" }
func (c *Context) Int(name string) int       { return int(c.Int64(name)) }
func (c *Context) IsSet(name string) bool    { return c.set[name] }

// The flag package already rejected anything that does not parse.
func (c *Context) Int64(name string) int64 {
	value, _ := strconv.ParseInt(c.String(name), 10, 64)
	return value
}

func (c *Context) Uint64(name string) uint64 {
	value, _ := strconv.ParseUint(c.String(name), 10, 64)
	return value
}

// Run runs the command with the args that follow its name.
func (cmd *Command) Run(ctx context.Context, args []string) error {
	if cmd.Action == nil {
		if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
			cmd.help()
			return nil
		}
		for _, sub := range cmd.Subcommands {
			if sub.Name == args[0] {
				return sub.Run(ctx, args[1:])
			}
		}
		return fmt.Errorf("%s: unknown command %q", cmd.Name, args[0])
	}

	fs := flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	for _, f := range cmd.Flags {
		f.register(fs)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	// parsing stops at the first non flag, so anything left over is a flag that
	// was silently dropped - "" ahead of --is_put, say
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", cmd.Name, fs.Arg(0))
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, f := range cmd.Flags {
		if err := f.check(set); err != nil {
			return err
		}
	}
	return cmd.Action(&Context{Context: ctx, fs: fs, set: set})
}

// help goes to stderr: inside connect, stdout carries the server's messages.
func (cmd *Command) help() {
	fmt.Fprintf(os.Stderr, "usage: %s <command> [flags]\n\ncommands:\n", cmd.Name)
	for _, sub := range cmd.Subcommands {
		fmt.Fprintf(os.Stderr, "  %-14s %s\n", sub.Name, sub.Usage)
	}
}
