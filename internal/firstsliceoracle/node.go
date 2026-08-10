// Package firstsliceoracle owns the REL-002a Node differential boundary.
package firstsliceoracle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

const LockedNodeVersion = "v22.22.0"

const nodeAddScript = `const [a,b]=process.argv.slice(1);const fromBits=(h)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};process.stdout.write(toBits(fromBits(a)+fromBits(b))+"\n");`
const nodeComputeScript = `const [a,b]=process.argv.slice(1);const fromBits=(h)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};function add(left,right){return left+right}function compute(left,right){let value=add(left,right);value=value+right;return value}process.stdout.write(toBits(compute(fromBits(a),fromBits(b)))+"\n");`
const nodeLoopScript = `const [a,b]=process.argv.slice(1);const fromBits=(h)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};function compute(step,limit){let value=step;while(value<limit){value=value+step}return value}process.stdout.write(toBits(compute(fromBits(a),fromBits(b)))+"\n");`
const nodeClassifyScript = `const [a]=process.argv.slice(1);const fromBits=(h)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};function classify(value){if(value<0){return -1}if(value<1){return 0}return 1}process.stdout.write(toBits(classify(fromBits(a)))+"\n");`
const nodeChooseScript = `const [flag,a,b]=process.argv.slice(1);if(flag!=="true"&&flag!=="false")process.exit(2);const x=new ArrayBuffer(8),v=new DataView(x);const fromBits=(h)=>{v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};process.stdout.write(toBits(fromBits(flag==="true"?a:b))+"\n");`
const nodeCoalesceScript = `const [tag,a,b]=process.argv.slice(1);if(tag!=="number"&&tag!=="null"&&tag!=="undefined")process.exit(2);const x=new ArrayBuffer(8),v=new DataView(x);const fromBits=(h)=>{v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};const value=tag==="number"?fromBits(a):tag==="null"?null:undefined;process.stdout.write(toBits(value??fromBits(b))+"\n");`
const nodeCoalesceAssignScript = `const [tag,a,b]=process.argv.slice(1);if(tag!=="number"&&tag!=="null"&&tag!=="undefined")process.exit(2);const x=new ArrayBuffer(8),v=new DataView(x);const fromBits=(h)=>{v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const toBits=(n)=>{v.setFloat64(0,n,false);return v.getBigUint64(0,false).toString(16).padStart(16,"0")};let value=tag==="number"?fromBits(a):tag==="null"?null:undefined;value??=fromBits(b);process.stdout.write(toBits(value)+"\n");`

type NodeOracle struct {
	path       string
	version    string
	scriptHash string
}

type Result struct {
	Arguments  []string `json:"arguments"`
	Output     []byte   `json:"-"`
	OutputHash string   `json:"outputHash"`
}

func OpenNode(ctx context.Context, path string) (*NodeOracle, error) {
	if ctx == nil {
		return nil, fmt.Errorf("oracle context is nil")
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("Node executable path is empty")
	}
	command := exec.CommandContext(ctx, path, "--version")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("query Node version: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if version != LockedNodeVersion {
		return nil, fmt.Errorf("unsupported Node version %q, want %s", version, LockedNodeVersion)
	}
	return &NodeOracle{path: path, version: version, scriptHash: hashBytes([]byte(nodeAddScript))}, nil
}

func (oracle *NodeOracle) Version() string {
	if oracle == nil {
		return ""
	}
	return oracle.version
}

func (oracle *NodeOracle) ScriptHash() string {
	if oracle == nil {
		return ""
	}
	return oracle.scriptHash
}

func (oracle *NodeOracle) Add(ctx context.Context, left, right string) (Result, error) {
	return oracle.run(ctx, nodeAddScript, left, right)
}

func (oracle *NodeOracle) Compute(ctx context.Context, left, right string) (Result, error) {
	return oracle.run(ctx, nodeComputeScript, left, right)
}

func (oracle *NodeOracle) Loop(ctx context.Context, step, limit string) (Result, error) {
	return oracle.run(ctx, nodeLoopScript, step, limit)
}

func (oracle *NodeOracle) Classify(ctx context.Context, value string) (Result, error) {
	return oracle.run(ctx, nodeClassifyScript, value)
}

// Choose evaluates the boolean branch using the locked Node oracle.
func (oracle *NodeOracle) Choose(ctx context.Context, flag bool, left, right string) (Result, error) {
	value := "false"
	if flag {
		value = "true"
	}
	return oracle.run(ctx, nodeChooseScript, value, left, right)
}

func (oracle *NodeOracle) Coalesce(ctx context.Context, tag, value, fallback string) (Result, error) {
	return oracle.run(ctx, nodeCoalesceScript, tag, value, fallback)
}

func (oracle *NodeOracle) CoalesceAssign(ctx context.Context, tag, value, fallback string) (Result, error) {
	return oracle.run(ctx, nodeCoalesceAssignScript, tag, value, fallback)
}

func (oracle *NodeOracle) run(ctx context.Context, script string, arguments ...string) (Result, error) {
	if oracle == nil || strings.TrimSpace(oracle.path) == "" {
		return Result{}, fmt.Errorf("Node oracle is not initialized")
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("oracle context is nil")
	}
	if len(arguments) < 1 {
		return Result{}, fmt.Errorf("Node oracle requires at least one argument")
	}
	for _, argument := range arguments {
		if argument == "true" || argument == "false" || argument == "number" || argument == "null" || argument == "undefined" {
			continue
		}
		if !isBits(argument) {
			return Result{}, fmt.Errorf("Node oracle arguments must be canonical binary64 hex")
		}
	}
	if script == nodeChooseScript && arguments[0] != "true" && arguments[0] != "false" {
		return Result{}, fmt.Errorf("Node oracle arguments must be canonical binary64 hex")
	}
	command := exec.CommandContext(ctx, oracle.path, "-e", script, "--")
	command.Args = append(command.Args, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("run Node oracle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(trimmed, "\n") || !isBits(trimmed) {
		return Result{}, fmt.Errorf("Node oracle output is not one canonical binary64 line: %q", output)
	}
	return Result{Arguments: slices.Clone(arguments), Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func ScriptHash() string { return hashBytes([]byte(nodeAddScript)) }

func ComputeScriptHash() string { return hashBytes([]byte(nodeComputeScript)) }

func LoopScriptHash() string { return hashBytes([]byte(nodeLoopScript)) }

func ClassifyScriptHash() string { return hashBytes([]byte(nodeClassifyScript)) }

// ChooseScriptHash returns the locked script identity for boolean choose.
func ChooseScriptHash() string { return hashBytes([]byte(nodeChooseScript)) }

func CoalesceScriptHash() string { return hashBytes([]byte(nodeCoalesceScript)) }

func CoalesceAssignScriptHash() string { return hashBytes([]byte(nodeCoalesceAssignScript)) }

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func isBits(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
