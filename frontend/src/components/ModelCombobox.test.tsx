import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import ModelCombobox from "./ModelCombobox";
import * as hfApi from "../hfApi";

vi.mock("../hfApi");

const mockSearchModels = vi.mocked(hfApi.searchModels);
const mockGetModelDetail = vi.mocked(hfApi.getModelDetail);

beforeEach(() => {
  vi.resetAllMocks();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

const sampleResults: hfApi.HfModelSummary[] = [
  {
    modelId: "meta-llama/Llama-3.1-8B-Instruct",
    downloads: 6500000,
    likes: 5500,
    private: false,
  },
  {
    modelId: "meta-llama/Llama-3.1-70B-Instruct",
    downloads: 3200000,
    likes: 4100,
    private: false,
  },
  {
    modelId: "TinyLlama/TinyLlama-1.1B-Chat-v1.0",
    downloads: 1000000,
    likes: 800,
    private: false,
  },
];

// Helper: simulate typing in a controlled combobox by firing change + rerendering.
function typeAndRerender(
  rerender: (ui: React.ReactElement) => void,
  text: string,
  props: Partial<React.ComponentProps<typeof ModelCombobox>> = {}
) {
  const input = screen.getByPlaceholderText(/Search models/);
  fireEvent.change(input, { target: { value: text } });
  rerender(
    <ModelCombobox
      value={text}
      onChange={props.onChange ?? (() => {})}
      onModelSelect={props.onModelSelect}
      hfToken={props.hfToken}
    />
  );
}

describe("ModelCombobox", () => {
  it("renders an input field", () => {
    render(<ModelCombobox value="" onChange={() => {}} />);
    expect(screen.getByPlaceholderText(/Search models/)).toBeInTheDocument();
  });

  it("calls onChange when user types", () => {
    const onChange = vi.fn();
    render(<ModelCombobox value="" onChange={onChange} />);
    const input = screen.getByPlaceholderText(/Search models/);
    fireEvent.change(input, { target: { value: "llama" } });
    expect(onChange).toHaveBeenCalledWith("llama");
  });

  it("shows dropdown after debounce", async () => {
    mockSearchModels.mockResolvedValue(sampleResults);

    const { rerender } = render(
      <ModelCombobox value="" onChange={() => {}} />
    );

    typeAndRerender(rerender, "llama");

    // Advance past the 300ms debounce.
    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    expect(mockSearchModels).toHaveBeenCalledWith("llama", undefined);

    // Let the resolved promise flush.
    await act(async () => {
      await Promise.resolve();
    });

    expect(
      screen.getByText("meta-llama/Llama-3.1-8B-Instruct")
    ).toBeInTheDocument();
    expect(
      screen.getByText("meta-llama/Llama-3.1-70B-Instruct")
    ).toBeInTheDocument();
  });

  it("displays download counts and likes", async () => {
    mockSearchModels.mockResolvedValue(sampleResults);

    const { rerender } = render(
      <ModelCombobox value="" onChange={() => {}} />
    );

    typeAndRerender(rerender, "llama");

    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    await act(async () => {
      await Promise.resolve();
    });

    expect(screen.getByText("6.5M downloads")).toBeInTheDocument();
    expect(screen.getByText("3.2M downloads")).toBeInTheDocument();
    expect(screen.getByText("5.5K likes")).toBeInTheDocument();
    expect(screen.getByText("4.1K likes")).toBeInTheDocument();
  });

  it("calls onModelSelect with detail when item selected", async () => {
    mockSearchModels.mockResolvedValue(sampleResults);
    mockGetModelDetail.mockResolvedValue({
      ...sampleResults[0],
      sha: "abc123def",
      gated: "manual",
    });

    const onChange = vi.fn();
    const onModelSelect = vi.fn();

    const { rerender } = render(
      <ModelCombobox
        value=""
        onChange={onChange}
        onModelSelect={onModelSelect}
      />
    );

    typeAndRerender(rerender, "llama", { onChange, onModelSelect });

    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    await act(async () => {
      await Promise.resolve();
    });

    // Click on the first result.
    fireEvent.mouseDown(
      screen.getByText("meta-llama/Llama-3.1-8B-Instruct")
    );

    expect(onChange).toHaveBeenCalledWith(
      "meta-llama/Llama-3.1-8B-Instruct"
    );

    // Let the detail fetch resolve.
    await act(async () => {
      await Promise.resolve();
    });

    expect(onModelSelect).toHaveBeenCalledWith(
      expect.objectContaining({ sha: "abc123def" })
    );
  });

  it("passes hfToken to search", async () => {
    mockSearchModels.mockResolvedValue([]);

    const { rerender } = render(
      <ModelCombobox value="" onChange={() => {}} hfToken="hf_mytoken" />
    );

    typeAndRerender(rerender, "llama", { hfToken: "hf_mytoken" });

    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    expect(mockSearchModels).toHaveBeenCalledWith("llama", "hf_mytoken");
  });

  it("does not search for single character input", async () => {
    mockSearchModels.mockResolvedValue([]);

    const { rerender } = render(
      <ModelCombobox value="" onChange={() => {}} />
    );

    typeAndRerender(rerender, "l");

    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    expect(mockSearchModels).not.toHaveBeenCalled();
  });

  it("shows gated warning on 403", async () => {
    mockSearchModels.mockResolvedValue([sampleResults[0]]);
    mockGetModelDetail.mockRejectedValue(new Error("gated"));

    const { rerender } = render(
      <ModelCombobox value="" onChange={() => {}} />
    );

    typeAndRerender(rerender, "llama");

    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    await act(async () => {
      await Promise.resolve();
    });

    fireEvent.mouseDown(
      screen.getByText("meta-llama/Llama-3.1-8B-Instruct")
    );

    await act(async () => {
      await Promise.resolve();
    });

    expect(screen.getByText(/requires access approval/)).toBeInTheDocument();
  });

  it("closes dropdown on outside click", async () => {
    mockSearchModels.mockResolvedValue(sampleResults);

    const { rerender } = render(
      <ModelCombobox value="" onChange={() => {}} />
    );

    typeAndRerender(rerender, "llama");

    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    await act(async () => {
      await Promise.resolve();
    });

    expect(
      screen.getByText("meta-llama/Llama-3.1-8B-Instruct")
    ).toBeInTheDocument();

    // Click outside.
    fireEvent.mouseDown(document.body);

    expect(
      screen.queryByText("meta-llama/Llama-3.1-8B-Instruct")
    ).not.toBeInTheDocument();
  });
});
