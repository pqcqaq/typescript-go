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
const nodeStringLengthScript = `const [h]=process.argv.slice(1);if(!/^(?:[0-9a-f]{4})*$/.test(h))process.exit(2);let s="";for(let i=0;i<h.length;i+=4)s+=String.fromCharCode(parseInt(h.slice(i,i+4),16));const x=new ArrayBuffer(8),v=new DataView(x);v.setFloat64(0,s.length,false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeObjectAliasScript = `const [h]=process.argv.slice(1);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const value=v.getFloat64(0,false);const object={value};const alias=object;alias.value=alias.value+1;v.setFloat64(0,object.value,false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodePropertyNullishAssignScript = `const [tag,h]=process.argv.slice(1);if(tag!=="number"&&tag!=="null"&&tag!=="undefined")process.exit(2);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const value=tag==="number"?v.getFloat64(0,false):tag==="null"?null:undefined;const object={backing:value,get result(){return this.backing},set result(next){this.backing=next}};const key="result";const result=(object[key]??=1);v.setFloat64(0,result,false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeClosureCounterScript = `const [h]=process.argv.slice(1);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const start=v.getFloat64(0,false);function makeCounter(value){let count=value;return()=>{count+=1;return count}}const increment=makeCounter(start);v.setFloat64(0,increment()+increment(),false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeClassCounterScript = `const [h]=process.argv.slice(1);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const start=v.getFloat64(0,false);class Counter{value=0;constructor(value){this.value=value}increment(){this.value+=1;return this.value}}const counter=new Counter(start);v.setFloat64(0,counter.increment()+counter.increment(),false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeDerivedCounterScript = `const cv=h=>{const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);return v.getFloat64(0,false)};const [a,b]=process.argv.slice(1),start=cv(a),step=cv(b),x=new ArrayBuffer(8),v=new DataView(x);class Counter{value=0;constructor(value){this.value=value}}class StepCounter extends Counter{step=1;constructor(value,step){super(value);this.step=step}increment(){this.value+=this.step;return this.value}}const counter=new StepCounter(start,step);v.setFloat64(0,counter.increment()+counter.increment(),false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeClassAccessScript = `const x=new ArrayBuffer(8),v=new DataView(x);class Vault{secret=1;value=2;readSecret(other){return other.secret}}class DerivedVault extends Vault{readValue(other){return other.value}}const vault=new DerivedVault;v.setFloat64(0,vault.readSecret(vault)+vault.readValue(vault),false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeObjectViewScript = `const [h]=process.argv.slice(1);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const source={value:v.getFloat64(0,false)};const view=source;v.setFloat64(0,view.value,false);process.stdout.write(v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeObjectLayoutCopyScript = `const [h]=process.argv.slice(1);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const source={value:v.getFloat64(0,false)};const copy={value:source.value};source.value=1;v.setFloat64(0,copy.value,false);process.stdout.write((copy!==source?"1":"0")+":"+v.getBigUint64(0,false).toString(16).padStart(16,"0")+"\n");`
const nodeObjectAccessorViewScript = `const [tag,h]=process.argv.slice(1);if(tag!=="number"&&tag!=="null"&&tag!=="undefined")process.exit(2);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const payload=v.getFloat64(0,false);const source={backing:tag==="number"?payload:tag==="null"?null:undefined,get result(){return this.backing}};const view=source,result=view.result,outTag=result===null?"1":result===undefined?"2":"0";if(outTag==="0")v.setFloat64(0,result,false);process.stdout.write(outTag+":"+(outTag==="0"?v.getBigUint64(0,false).toString(16).padStart(16,"0"):h)+"\n");`
const nodeCheckedObjectCastScript = `const [shape,h]=process.argv.slice(1);if(!["matching","missing","extra","accessor"].includes(shape)||!/^[0-9a-f]{16}$/.test(h))process.exit(2);const x=new ArrayBuffer(8),v=new DataView(x);v.setBigUint64(0,BigInt("0x"+h),false);const payload=v.getFloat64(0,false);let source;if(shape==="matching")source={value:payload};else if(shape==="missing")source={};else if(shape==="extra")source={value:payload,extra:1};else source={get value(){return payload}};const d=Object.getOwnPropertyDescriptor(source,"value"),match=!!d&&Object.prototype.hasOwnProperty.call(d,"value");if(match)v.setFloat64(0,source.value,false);process.stdout.write((match?"1":"0")+":"+(match?v.getBigUint64(0,false).toString(16).padStart(16,"0"):h)+"\n");`

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

func (oracle *NodeOracle) StringLength(ctx context.Context, codeUnits string) (Result, error) {
	if !isUTF16CodeUnits(codeUnits) {
		return Result{}, fmt.Errorf("Node oracle UTF-16 argument is not canonical code-unit hex")
	}
	return oracle.runUTF16(ctx, nodeStringLengthScript, codeUnits)
}

func (oracle *NodeOracle) ObjectAlias(ctx context.Context, value string) (Result, error) {
	return oracle.run(ctx, nodeObjectAliasScript, value)
}

func (oracle *NodeOracle) PropertyNullishAssign(ctx context.Context, tag, value string) (Result, error) {
	return oracle.run(ctx, nodePropertyNullishAssignScript, tag, value)
}

func (oracle *NodeOracle) ClosureCounter(ctx context.Context, value string) (Result, error) {
	return oracle.run(ctx, nodeClosureCounterScript, value)
}

func (oracle *NodeOracle) ClassCounter(ctx context.Context, value string) (Result, error) {
	return oracle.run(ctx, nodeClassCounterScript, value)
}

func (oracle *NodeOracle) DerivedCounter(ctx context.Context, start, step string) (Result, error) {
	return oracle.run(ctx, nodeDerivedCounterScript, start, step)
}

func (oracle *NodeOracle) ClassAccess(ctx context.Context) (Result, error) {
	return oracle.run(ctx, nodeClassAccessScript)
}

func (oracle *NodeOracle) ObjectView(ctx context.Context, value string) (Result, error) {
	return oracle.run(ctx, nodeObjectViewScript, value)
}

func (oracle *NodeOracle) ObjectLayoutCopy(ctx context.Context, value string) (Result, error) {
	if oracle == nil || strings.TrimSpace(oracle.path) == "" {
		return Result{}, fmt.Errorf("Node oracle is not initialized")
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("oracle context is nil")
	}
	if !isBits(value) {
		return Result{}, fmt.Errorf("Node object-layout-copy payload is not canonical binary64 hex")
	}
	command := exec.CommandContext(ctx, oracle.path, "-e", nodeObjectLayoutCopyScript, "--", value)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("run Node object-layout-copy oracle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if len(trimmed) != 18 || (trimmed[0] != '0' && trimmed[0] != '1') || trimmed[1] != ':' || !isBits(trimmed[2:]) {
		return Result{}, fmt.Errorf("Node object-layout-copy output is invalid: %q", output)
	}
	return Result{Arguments: []string{value}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func (oracle *NodeOracle) ObjectAccessorView(ctx context.Context, tag, value string) (Result, error) {
	if oracle == nil || strings.TrimSpace(oracle.path) == "" {
		return Result{}, fmt.Errorf("Node oracle is not initialized")
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("oracle context is nil")
	}
	if tag != "number" && tag != "null" && tag != "undefined" {
		return Result{}, fmt.Errorf("Node accessor-view tag is invalid")
	}
	if !isBits(value) {
		return Result{}, fmt.Errorf("Node accessor-view payload is not canonical binary64 hex")
	}
	command := exec.CommandContext(ctx, oracle.path, "-e", nodeObjectAccessorViewScript, "--", tag, value)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("run Node accessor-view oracle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if len(trimmed) != 18 || (trimmed[0] < '0' || trimmed[0] > '2') || trimmed[1] != ':' || !isBits(trimmed[2:]) {
		return Result{}, fmt.Errorf("Node accessor-view output is invalid: %q", output)
	}
	arguments := []string{tag, value}
	return Result{Arguments: arguments, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func (oracle *NodeOracle) CheckedObjectCast(ctx context.Context, shape, value string) (Result, error) {
	if oracle == nil || strings.TrimSpace(oracle.path) == "" {
		return Result{}, fmt.Errorf("Node oracle is not initialized")
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("oracle context is nil")
	}
	if shape != "matching" && shape != "missing" && shape != "extra" && shape != "accessor" {
		return Result{}, fmt.Errorf("Node checked-cast shape is invalid")
	}
	if !isBits(value) {
		return Result{}, fmt.Errorf("Node checked-cast payload is not canonical binary64 hex")
	}
	command := exec.CommandContext(ctx, oracle.path, "-e", nodeCheckedObjectCastScript, "--", shape, value)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("run Node checked-cast oracle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if len(trimmed) != 18 || (trimmed[0] != '0' && trimmed[0] != '1') || trimmed[1] != ':' || !isBits(trimmed[2:]) {
		return Result{}, fmt.Errorf("Node checked-cast output is invalid: %q", output)
	}
	return Result{Arguments: []string{shape, value}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func (oracle *NodeOracle) run(ctx context.Context, script string, arguments ...string) (Result, error) {
	if oracle == nil || strings.TrimSpace(oracle.path) == "" {
		return Result{}, fmt.Errorf("Node oracle is not initialized")
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("oracle context is nil")
	}
	if len(arguments) < 1 && script != nodeClassAccessScript {
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

func (oracle *NodeOracle) runUTF16(ctx context.Context, script, codeUnits string) (Result, error) {
	if oracle == nil || strings.TrimSpace(oracle.path) == "" {
		return Result{}, fmt.Errorf("Node oracle is not initialized")
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("oracle context is nil")
	}
	command := exec.CommandContext(ctx, oracle.path, "-e", script, "--", codeUnits)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("run Node UTF-16 oracle: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(trimmed, "\n") || !isBits(trimmed) {
		return Result{}, fmt.Errorf("Node UTF-16 oracle output is not one canonical binary64 line: %q", output)
	}
	return Result{Arguments: []string{codeUnits}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func ScriptHash() string { return hashBytes([]byte(nodeAddScript)) }

func ComputeScriptHash() string { return hashBytes([]byte(nodeComputeScript)) }

func LoopScriptHash() string { return hashBytes([]byte(nodeLoopScript)) }

func ClassifyScriptHash() string { return hashBytes([]byte(nodeClassifyScript)) }

// ChooseScriptHash returns the locked script identity for boolean choose.
func ChooseScriptHash() string { return hashBytes([]byte(nodeChooseScript)) }

func CoalesceScriptHash() string { return hashBytes([]byte(nodeCoalesceScript)) }

func CoalesceAssignScriptHash() string { return hashBytes([]byte(nodeCoalesceAssignScript)) }

func StringLengthScriptHash() string { return hashBytes([]byte(nodeStringLengthScript)) }

func ObjectAliasScriptHash() string { return hashBytes([]byte(nodeObjectAliasScript)) }

func PropertyNullishAssignScriptHash() string {
	return hashBytes([]byte(nodePropertyNullishAssignScript))
}

func ClosureCounterScriptHash() string { return hashBytes([]byte(nodeClosureCounterScript)) }

func ClassCounterScriptHash() string       { return hashBytes([]byte(nodeClassCounterScript)) }
func DerivedCounterScriptHash() string     { return hashBytes([]byte(nodeDerivedCounterScript)) }
func ClassAccessScriptHash() string        { return hashBytes([]byte(nodeClassAccessScript)) }
func ObjectViewScriptHash() string         { return hashBytes([]byte(nodeObjectViewScript)) }
func ObjectLayoutCopyScriptHash() string   { return hashBytes([]byte(nodeObjectLayoutCopyScript)) }
func ObjectAccessorViewScriptHash() string { return hashBytes([]byte(nodeObjectAccessorViewScript)) }
func CheckedObjectCastScriptHash() string  { return hashBytes([]byte(nodeCheckedObjectCastScript)) }

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

func isUTF16CodeUnits(value string) bool {
	if len(value)%4 != 0 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
