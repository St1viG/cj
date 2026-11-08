import { type ComponentPropsWithoutRef, type ReactNode, useId } from "react";

interface Common {
  label: string;
  /** Help text, tied to the control for screen readers. */
  hint?: ReactNode;
}
type InputField = Common & { as?: "input" } & Omit<ComponentPropsWithoutRef<"input">, "id">;
type TextareaField = Common & { as: "textarea" } & Omit<ComponentPropsWithoutRef<"textarea">, "id">;
type SelectField = Common & { as: "select" } & Omit<ComponentPropsWithoutRef<"select">, "id">;
export type FieldProps = InputField | TextareaField | SelectField;

/**
 * A labelled control: `<Field label="Email" name="email" type="email" />`,
 * or `as="textarea"` / `as="select"` (options as children). Everything else
 * passes straight to the element.
 */
export function Field(props: FieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const describedBy = props.hint ? hintId : undefined;

  let control: ReactNode;
  if (props.as === "textarea") {
    const { as: _as, label: _label, hint: _hint, ...rest } = props;
    control = <textarea id={id} aria-describedby={describedBy} {...rest} />;
  } else if (props.as === "select") {
    const { as: _as, label: _label, hint: _hint, ...rest } = props;
    control = <select id={id} aria-describedby={describedBy} {...rest} />;
  } else {
    const { as: _as, label: _label, hint: _hint, ...rest } = props;
    control = <input id={id} aria-describedby={describedBy} {...rest} />;
  }

  return (
    <div className="fld">
      <label htmlFor={id}>{props.label}</label>
      {control}
      {props.hint && (
        <small className="msg" id={hintId}>
          {props.hint}
        </small>
      )}
    </div>
  );
}
