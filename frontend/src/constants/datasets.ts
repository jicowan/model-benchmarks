// Supported inference-perf dataset types. Shared between the new-benchmark
// Run form and the Configuration page's Seeding Matrix defaults so both pages
// offer the same options. Kept in sync manually with inference-perf's
// supported dataset list.
export interface DatasetOption {
  value: string;
  label: string;
  description: string;
}

export const datasetOptions: DatasetOption[] = [
  { value: "synthetic", label: "Synthetic", description: "Controlled input/output distributions" },
  { value: "sharegpt", label: "ShareGPT", description: "Real-world conversational data" },
  { value: "random", label: "Random", description: "Random token data" },
  { value: "shared_prefix", label: "Shared Prefix", description: "Prefix caching scenarios" },
  { value: "cnn_dailymail", label: "CNN DailyMail", description: "Summarization use cases" },
  { value: "billsum_conversations", label: "Billsum", description: "Long context prefill-heavy" },
  { value: "infinity_instruct", label: "Infinity Instruct", description: "Long context decode-heavy" },
];
