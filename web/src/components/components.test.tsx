import { Code, ConnectError } from "@connectrpc/connect";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Confirm } from "./Confirm";
import { DataTable } from "./DataTable";
import { Field } from "./Field";
import { Form } from "./Form";
import { QueryState } from "./State";

interface Row {
  id: string;
  name: string;
}
const columns = [
  { header: "Id", cell: (r: Row) => r.id },
  { header: "Name", cell: (r: Row) => r.name },
];
const rows: Row[] = [
  { id: "a", name: "alpha" },
  { id: "b", name: "beta" },
];

describe("DataTable", () => {
  it("renders rows under their headers", () => {
    render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} caption="Things" />);
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Id", "Name"]);
    expect(screen.getAllByRole("row")).toHaveLength(3);
    expect(screen.getByText("beta")).toBeTruthy();
    expect(screen.getByText("Things")).toBeTruthy();
  });

  it("shows loading until there are rows", () => {
    render(<DataTable columns={columns} rows={undefined} rowKey={(r) => r.id} loading />);
    expect(screen.getByRole("status").textContent).toBe("Loading…");
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows the empty message", () => {
    render(<DataTable columns={columns} rows={[]} rowKey={(r) => r.id} empty="No instances yet." />);
    expect(screen.getByText("No instances yet.")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows an error with a working retry", () => {
    const retry = vi.fn();
    render(
      <DataTable
        columns={columns}
        rows={undefined}
        rowKey={(r) => r.id}
        error={new ConnectError("database unavailable", Code.Unavailable)}
        onRetry={retry}
      />,
    );
    expect(screen.getByRole("alert").textContent).toContain("database unavailable");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it("keeps rows already loaded when a refetch fails", () => {
    render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} error={new ConnectError("blip", Code.Unavailable)} />);
    expect(screen.getByRole("alert").textContent).toContain("blip");
    expect(screen.getByText("alpha")).toBeTruthy();
  });
});

describe("QueryState", () => {
  const base = { isPending: false, isError: false, error: null, refetch: vi.fn() };

  it("renders the loading, error, empty and data cases", () => {
    const { rerender } = render(<QueryState query={{ ...base, isPending: true, data: undefined }}>{() => <p>data</p>}</QueryState>);
    expect(screen.getByRole("status")).toBeTruthy();

    rerender(
      <QueryState query={{ ...base, isError: true, error: new ConnectError("boom", Code.Internal), data: undefined }}>
        {() => <p>data</p>}
      </QueryState>,
    );
    expect(screen.getByRole("alert").textContent).toContain("boom");

    rerender(
      <QueryState query={{ ...base, data: [] as string[] }} isEmpty={(d) => d.length === 0} empty="Empty.">
        {() => <p>data</p>}
      </QueryState>,
    );
    expect(screen.getByText("Empty.")).toBeTruthy();

    rerender(
      <QueryState query={{ ...base, data: ["x"] }} isEmpty={(d) => d.length === 0}>
        {(d) => <p>{d.join()}</p>}
      </QueryState>,
    );
    expect(screen.getByText("x")).toBeTruthy();
  });
});

describe("Field", () => {
  it("ties the label and the hint to the control", () => {
    render(<Field label="Email" name="email" type="email" hint="Your work address" />);
    const input = screen.getByLabelText("Email") as HTMLInputElement;
    expect(input.name).toBe("email");
    expect(input.type).toBe("email");
    expect(input.getAttribute("aria-describedby")).toBe(screen.getByText("Your work address").id);
  });

  it("renders a textarea and a select", () => {
    render(
      <>
        <Field as="textarea" label="User data" name="user_data" rows={3} />
        <Field as="select" label="Image" name="image">
          <option value="a">A</option>
        </Field>
      </>,
    );
    expect(screen.getByLabelText("User data").tagName).toBe("TEXTAREA");
    expect(screen.getByLabelText("Image").tagName).toBe("SELECT");
  });
});

describe("Form", () => {
  it("passes the entered values and locks the fields while it works", async () => {
    let finish!: () => void;
    const onSubmit = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    render(
      <Form onSubmit={onSubmit} submitLabel="Save">
        <Field label="Name" name="name" />
      </Form>,
    );
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "web-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    const data = (onSubmit.mock.calls[0] as unknown as [FormData])[0];
    expect(data.get("name")).toBe("web-1");
    // The fieldset disables its controls; `.disabled` on the input would not show that.
    await waitFor(() => expect(screen.getByLabelText("Name").matches(":disabled")).toBe(true));
    expect(screen.getByRole("status").textContent).toBe("Working…");

    finish();
    await waitFor(() => expect(screen.getByLabelText("Name").matches(":disabled")).toBe(false));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows why a submit failed and lets the user try again", async () => {
    const onSubmit = vi.fn().mockRejectedValueOnce(new ConnectError("no capacity", Code.ResourceExhausted)).mockResolvedValue(undefined);
    render(
      <Form onSubmit={onSubmit}>
        <Field label="Name" name="name" />
      </Form>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));
    expect((await screen.findByRole("alert")).textContent).toBe("no capacity");

    fireEvent.click(screen.getByRole("button", { name: "Submit" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });
});

describe("Confirm", () => {
  it("does nothing until confirmed, and can be cancelled", async () => {
    const onConfirm = vi.fn();
    render(<Confirm label="Terminate" message="Terminate i1?" onConfirm={onConfirm} />);

    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    expect(screen.getByText("Terminate i1?")).toBeTruthy();
    expect(onConfirm).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Terminate" })).toBeTruthy();
    expect(onConfirm).not.toHaveBeenCalled();

    // Asking again: the trigger is replaced by the question and its confirm button.
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledOnce());
    // Done: back to the plain button.
    await waitFor(() => expect(screen.queryByText("Terminate i1?")).toBeNull());
  });

  it("keeps the question open and shows the error when the action fails", async () => {
    const onConfirm = vi.fn().mockRejectedValue(new ConnectError("already terminating", Code.FailedPrecondition));
    render(<Confirm label="Terminate" message="Terminate i1?" confirmLabel="Yes, terminate" onConfirm={onConfirm} />);
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, terminate" }));
    expect((await screen.findByRole("alert")).textContent).toContain("already terminating");
    expect(screen.getByText("Terminate i1?")).toBeTruthy();
  });
});
