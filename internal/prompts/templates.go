package prompts

var Templates = struct {
	FactExtraction           string
	URLContentFactExtraction string
	SummaryFactExtraction    string
	AutoPost                 string
	FactArchiving            struct {
		InstructionGeneral string
		InstructionUser    string
		Main               string
	}
	FactQuery string
	Summary   struct {
		InstructionUpdate string
		InstructionNew    string
		Main              string
	}
	ImageGeneration       string
	ImageRequestDetection string
	ImageGenerationReply  string
	FollowResponse        string
	FollowResponseAlready string
	ErrorMessage          string
	AssistantAnalysis     struct {
		Instruction  string
		OutputFormat string
	}
	IntentClassification string
	DailySummary         struct {
		Header      string
		Instruction string
	}
	BotProfileGeneration string
	FactConsolidation    string
}{
	FactExtraction: promptFactExtraction,

	URLContentFactExtraction: promptURLContentFactExtraction,
	SummaryFactExtraction:    promptSummaryFactExtraction,
	AutoPost:                 promptAutoPost,
	FactArchiving: struct {
		InstructionGeneral string
		InstructionUser    string
		Main               string
	}{
		InstructionGeneral: promptFactArchivingInstructionGeneral,
		InstructionUser:    promptFactArchivingInstructionUser,
		Main:               promptFactArchivingMain,
	},
	FactQuery: promptFactQuery,
	Summary: struct {
		InstructionUpdate string
		InstructionNew    string
		Main              string
	}{
		InstructionUpdate: promptSummaryInstructionUpdate,
		InstructionNew:    promptSummaryInstructionNew,
		Main:              promptSummaryMain,
	},
	ImageGeneration:       promptImageGeneration,
	ImageRequestDetection: promptImageRequestDetection,
	ImageGenerationReply:  promptImageGenerationReply,
	FollowResponse:        promptFollowResponse,
	FollowResponseAlready: promptFollowResponseAlready,
	ErrorMessage:          promptErrorMessage,
	AssistantAnalysis: struct {
		Instruction  string
		OutputFormat string
	}{
		Instruction:  promptAssistantAnalysisInstruction,
		OutputFormat: promptAssistantAnalysisOutputFormat,
	},
	IntentClassification: promptIntentClassification,
	DailySummary: struct {
		Header      string
		Instruction string
	}{
		Header:      promptDailySummaryHeader,
		Instruction: promptDailySummaryInstruction,
	},
	BotProfileGeneration: promptBotProfileGeneration,

	FactConsolidation: promptFactConsolidation,
}
