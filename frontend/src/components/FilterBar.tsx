import { useState } from "react";
import type { CatalogFilter } from "../types";

interface Props {
  onFilter: (filter: CatalogFilter) => void;
}

export default function FilterBar({ onFilter }: Props) {
  const [model, setModel] = useState("");
  const [modelFamily, setModelFamily] = useState("");
  const [instanceFamily, setInstanceFamily] = useState("");
  const [acceleratorType, setAcceleratorType] = useState("");

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    onFilter({
      model: model || undefined,
      model_family: modelFamily || undefined,
      instance_family: instanceFamily || undefined,
      accelerator_type: acceleratorType || undefined,
    });
  }

  function handleClear() {
    setModel("");
    setModelFamily("");
    setInstanceFamily("");
    setAcceleratorType("");
    onFilter({});
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="flex flex-wrap items-end gap-3 mb-6"
    >
      <div>
        <label className="block text-xs font-medium text-gray-500 mb-1">
          Model
        </label>
        <input
          type="text"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder="e.g. meta-llama/Llama-3.1-8B"
          className="block w-64 rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-500 mb-1">
          Model Family
        </label>
        <select
          value={modelFamily}
          onChange={(e) => setModelFamily(e.target.value)}
          className="block rounded-md border border-gray-300 px-3 py-2 text-sm"
        >
          <option value="">All</option>
          <option value="llama">Llama</option>
          <option value="mistral">Mistral</option>
          <option value="qwen">Qwen</option>
          <option value="gemma">Gemma</option>
          <option value="deepseek">DeepSeek</option>
          <option value="phi">Phi</option>
        </select>
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-500 mb-1">
          Instance Family
        </label>
        <select
          value={instanceFamily}
          onChange={(e) => setInstanceFamily(e.target.value)}
          className="block rounded-md border border-gray-300 px-3 py-2 text-sm"
        >
          <option value="">All</option>
          <option value="g5">g5</option>
          <option value="g6">g6</option>
          <option value="g6e">g6e</option>
          <option value="p4d">p4d</option>
          <option value="p5">p5</option>
          <option value="p5e">p5e</option>
          <option value="inf2">inf2</option>
          <option value="trn1">trn1</option>
          <option value="trn2">trn2</option>
        </select>
      </div>
      <div>
        <label className="block text-xs font-medium text-gray-500 mb-1">
          Accelerator
        </label>
        <select
          value={acceleratorType}
          onChange={(e) => setAcceleratorType(e.target.value)}
          className="block rounded-md border border-gray-300 px-3 py-2 text-sm"
        >
          <option value="">All</option>
          <option value="gpu">GPU</option>
          <option value="neuron">Neuron</option>
        </select>
      </div>
      <button
        type="submit"
        className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
      >
        Filter
      </button>
      <button
        type="button"
        onClick={handleClear}
        className="rounded-md bg-white px-4 py-2 text-sm font-medium text-gray-700 border border-gray-300 hover:bg-gray-50"
      >
        Clear
      </button>
    </form>
  );
}
