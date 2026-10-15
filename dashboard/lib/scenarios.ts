export const SCENARIO_TYPES = [
  "order_fulfilment",
  "user_onboarding",
  "money_transfer",
  "report_pipeline",
] as const;

export type ScenarioType = (typeof SCENARIO_TYPES)[number];

export type Scenario = {
  type: ScenarioType;
  label: string;
  short: string;
  pitch: string;
  button: string;
  steps: number;
};

export const DEFAULT_BATCH = 3;
export const BATCH_OPTIONS = [1, 3, 5] as const;
/** Durable pause between steps for order / transfer / report (seconds). */
export const PACE_SECONDS = 3;
/** Onboarding wait between welcome and tips emails. */
export const ONBOARDING_DELAY_SECONDS = 45;

export const scenarios: Scenario[] = [
  {
    type: "order_fulfilment",
    label: "Checkout an order",
    short: "Checkout",
    pitch: "Charge card → reserve stock → email receipt",
    button: "Run checkouts",
    steps: 3,
  },
  {
    type: "user_onboarding",
    label: "Sign up a new user",
    short: "Sign up",
    pitch: "Create account → welcome email → wait → tips email",
    button: "Run sign-ups",
    steps: 3,
  },
  {
    type: "money_transfer",
    label: "Send money",
    short: "Transfer",
    pitch: "Debit Alice → credit Bob (refund if credit fails)",
    button: "Run transfers",
    steps: 2,
  },
  {
    type: "report_pipeline",
    label: "Build a report",
    short: "Report",
    pitch: "Fetch → process → upload → notify",
    button: "Run reports",
    steps: 4,
  },
];

export function isScenarioType(value: string | null | undefined): value is ScenarioType {
  return !!value && (SCENARIO_TYPES as readonly string[]).includes(value);
}

export function scenarioByType(type: string): Scenario {
  return scenarios.find((item) => item.type === type) ?? scenarios[0];
}

export function typeLabel(type: string): string {
  return scenarioByType(type).short;
}
