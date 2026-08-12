package bingo

type firstSliceMIRFunctionSetVerifier struct {
	name    string
	matches func([]FirstSliceMIRFunction) bool
	verify  func([]FirstSliceMIRFunction) error
}

var firstSliceMIRFunctionSetVerifiers = [...]firstSliceMIRFunctionSetVerifier{
	singleMIRFunctionVerifier("number-add", "add", verifyNumberAddMIRFunction),
	singleMIRFunctionVerifier("boolean-choose", "choose", verifyBooleanChooseMIRFunction),
	singleMIRFunctionVerifier("number-classify", "classify", verifyClassifyMIRFunction),
	{
		name: "local-call",
		matches: func(functions []FirstSliceMIRFunction) bool {
			return len(functions) == 2 && functions[0].Name == "add" && functions[1].Name == "compute"
		},
		verify: verifyLocalCallMIRFunctions,
	},
	singleMIRFunctionVerifier("number-loop", "compute", verifyLoopMIRFunction),
	{
		name: "nullable-coalesce",
		matches: func(functions []FirstSliceMIRFunction) bool {
			return len(functions) == 1 && (functions[0].Name == "coalesce" || functions[0].Name == "coalesceAssign")
		},
		verify: func(functions []FirstSliceMIRFunction) error {
			return verifyCoalesceMIRFunction(functions[0])
		},
	},
	singleMIRFunctionVerifier("utf16-string-length", "stringLength", verifyStringLengthMIRFunction),
	singleMIRFunctionVerifier("application-main", "main", verifyApplicationMainMIRFunction),
}

func singleMIRFunctionVerifier(name, functionName string, verify func(FirstSliceMIRFunction) error) firstSliceMIRFunctionSetVerifier {
	return firstSliceMIRFunctionSetVerifier{
		name: name,
		matches: func(functions []FirstSliceMIRFunction) bool {
			return len(functions) == 1 && functions[0].Name == functionName
		},
		verify: func(functions []FirstSliceMIRFunction) error {
			return verify(functions[0])
		},
	}
}
