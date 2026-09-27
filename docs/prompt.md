## 1. Create a markdown file explaining existing code

Use this when the goal is to understand code that already exists.

Read the relevant code and explain the code flow. Create a markdown file for your output at:

<OUTPUT_PATH>

If I provide a file, folder, branch, PR number, URL, Linear ticket, or feature name, use that as the scope. Otherwise, infer the smallest relevant scope from my request. Read the files directly. Run relevant tests if practical and include the result.

Focus on explaining code flow, not reviewing for bugs unless I explicitly ask for review.

Use these sections:

1. Intro And Overview
   - What this code does
   - The main user-facing or caller-facing behavior
   - The main packages/files involved
   - Any notable temporary choices, deviations, or limits

2. High-Level Code Flow
   Explain the sequence in plain English, but be explicit about handoffs between files.

   Start this section with a Mermaid sequence diagram showing the file/function call flow.
   The diagram must be a real sequence diagram, not a loose summary. Include a participant
   when there is a user/external caller, label participants with clear file/module/function
   names, show runtime wiring, loops, important data handoffs, success paths, and
   error/completion paths when relevant. Keep Mermaid syntax conservative: use
   `participant User` instead of `actor User`, avoid `autonumber`, avoid braces in message
   labels, avoid extra colons in notes, avoid HTML labels, and avoid punctuation-heavy
   labels if the renderer may not support them.

   Example shape:

   ```mermaid
   sequenceDiagram
       participant User
       participant Main
       participant Flow
       participant Adapter
       participant External

       User->>Main: Run command
       Main->>Flow: Build and call flow
       Flow->>Adapter: Call adapter
       Adapter->>External: Send request
       alt success
           External-->>Adapter: Success response
           Adapter-->>Flow: Result
           Flow-->>Main: Success
       else failure
           External-->>Adapter: Failure response
           Adapter-->>Flow: Error
           Flow-->>Main: Failure
       end
   ```

   For each step, include:
   - The file and function currently active
   - A 1-2 line description of what that file does or what knowledge it owns
   - Which previous file/function called it
   - What arguments, data, or information were passed in
   - Why that information is passed
   - What this step produces or calls next
   - How this file relates to the next file in the sequence

   Keep this section high-level. Do not quote code blocks here. Explain the flow like:
   "`cmd/travis/main.go` calls `runChat` after the user selects `chat`. `runChat` builds the provider, event bus, assistant conversation, and chat terminal, then passes them into `chat.BuildChatSession`. `internal/app/chat/chat.go` owns the interactive chat flow and calls `assistantConversation.RunAssistantTurn` directly for each prompt."

3. Low-Level Code Rundown
   Follow the same numbered flow from section 2.

   For each step, include:
   - The exact file and function/type names
   - A short current-code excerpt for the relevant code involved, preceded by the file/function/type and line link when available
   - A plain-English explanation of the current code
   - The important arguments and return values
   - The data structures passed between layers
   - The function called next
   - The relevant error behavior
   - The tests that cover this step, if any

   Make the relationship between files explicit. For example:
   "`cmd/travis/main.go::runChat` builds `chatTerminal := terminal.BuildTerminal(in, out)` and passes it to `chat.BuildChatSession(events, chatTerminal, assistantConversation)`.
   This means `internal/app/chat` owns the prompt/response loop, while `internal/frontends/terminal` only owns terminal input and output."

Also:

- Use clickable markdown links for local files when practical
- Prefer the actual call/data sequence over package-by-package summaries
- Call out ownership boundaries and dependency direction where they affect the flow
- Do not skip small glue functions if they are important to understanding how one file reaches the next
- When the prose mentions a concrete code symbol, type, struct field, helper, interface method, or enum value that is important to the explanation, show the relevant code excerpt nearby with a file/function/type link. Do not leave important code references only as prose.
- In code excerpts or illustrative snippets, use role-revealing local names. Avoid generic names like `converted`, `result`, `data`, `tmp`, `val`, `item`, or `gotBody` unless the exact current code already uses them. For examples and proposed code, name values after what they contain or capture, such as `chatMessages`, `chatTools`, `capturedRequestBody`, or `providerResponse`.
- If exact current code uses a generic name that makes the flow harder to understand, keep the excerpt accurate but explain the name briefly and, when relevant, call out the clearer name that should be used in the planned change.

## 2. Create a markdown file explaining a planned PR before it is written

Use this when the goal is to understand what a PR should change before the code is implemented.

Read the current code, the relevant ticket/plan/docs, and any branch metadata I provide. Create a markdown file for your output at:

<OUTPUT_PATH>

If I provide a Linear ticket, planned PR name, branch, PR number, URL, or file path, use that as the scope. Make it explicit whether the code is already implemented or still planned. Run tests only if they help verify the current baseline; this document is mainly about explaining the proposed change.

Focus on explaining the current code flow and the planned change. Do not review for bugs unless I explicitly ask for review.

Use these sections:

1. Intro And Overview
   - What the planned PR should add or change
   - What currently exists
   - The main user-facing or caller-facing behavior after the change
   - The main packages/files expected to change
   - The surrounding packages/files that are relevant for understanding the flow
   - Any notable temporary choices, deviations, or limits

2. High-Level Code Flow Relevant To This PR, And How It Changes
   Explain the relevant sequence in plain English, but be explicit about handoffs between files.

   Start this section with a Mermaid sequence diagram showing the relevant current flow
   and where the planned PR changes the path. The diagram must be a real sequence
   diagram, not a loose summary. Include a participant when there is a user/external caller,
   label participants with clear file/module/function names, show runtime wiring, loops,
   important data handoffs, success paths, and error/completion paths when relevant.
   Keep Mermaid syntax conservative: use `participant User` instead of `actor User`,
   avoid `autonumber`, avoid braces in message labels, avoid extra colons in notes,
   avoid HTML labels, and avoid punctuation-heavy labels if the renderer may not support
   them. For planned PR docs, add Mermaid `Note` blocks that explicitly mark current
   behavior versus planned behavior at the points where the code path changes.

   Example shape:

   ```mermaid
   sequenceDiagram
       participant User
       participant Main
       participant Flow
       participant Adapter
       participant External
       participant Tests

       User->>Main: Run command
       Main->>Flow: Build and call flow
       Flow->>Adapter: Call adapter
       Note over Flow,Adapter: Current request has existing fields only
       Note over Flow,Adapter: Planned request has new data
       Adapter->>External: Send request
       alt success
           External-->>Adapter: Success response
           Adapter-->>Flow: Result with planned data
       else failure
           External-->>Adapter: Failure response
           Adapter-->>Flow: Planned error
       end
       Tests->>Adapter: Fake external exchange
       Adapter-->>Tests: Captured result or error
   ```

   For each step, include:
   - The file and function currently active
   - A 1-2 line description of what that file does or what knowledge it owns
   - Which previous file/function calls it today
   - What arguments, data, or information are passed today
   - Why that information is passed
   - What this step currently produces or calls next
   - What the planned PR should change at this step
   - How this step should relate to the next file/function after the change

   Clearly label current behavior versus planned behavior. Keep this section high-level. Do not quote code blocks here.

3. Low-Level Code Rundown Matching Section 2
   Follow the exact same numbered flow from section 2.

   For each step, include two adjacent subsections:

   **Current Code**
   - The exact file and function/type names
   - A short current-code excerpt for the relevant code involved
   - A plain-English explanation of what the current code does
   - The important arguments, return values, and data structures currently passed between layers
   - The function called next today
   - The current error behavior
   - The tests that currently cover this step, if any

   **Planned Change**
   - What should change in this same file/function/type, or what new file/function/type should be introduced
   - A `Proposed code` block showing the new or changed code when code is involved, preceded by the target file/function/type and line link when available
   - If exact implementation is still undecided, show the smallest plausible code sketch and label it as illustrative
   - How arguments, return values, or data structures should change
   - The function that should be called next after the change
   - The expected error behavior after the change
   - The tests that should be added or updated

   Make the relationship between files explicit. The reader should be able to compare the current code and planned change without jumping to another section.

Also:

- Use clickable markdown links for local files when practical
- Prefer the relevant call/data sequence over package-by-package summaries
- Call out ownership boundaries and dependency direction where they affect the flow
- Do not skip small glue functions if they are important to understanding how one file reaches the next
- Clearly label current behavior versus planned/new behavior so the reader does not confuse existing code with code that still needs to be implemented
- When the prose mentions a concrete code symbol, type, struct field, helper, interface method, or enum value that is important to the planned change, show the current definition or proposed code nearby with a file/function/type link. Do not write bullets like "`core.ToolCallPart` becomes `tool_calls`" unless the relevant source type and adapter conversion code are shown close to that explanation.
- In proposed code, illustrative snippets, test sketches, and Mermaid labels, use role-revealing names. Avoid generic names like `converted`, `result`, `data`, `tmp`, `val`, `item`, or `gotBody`. Name values after their purpose and layer, such as `chatMessages`, `chatTools`, `capturedRequestBody`, `providerResponse`, `toolCallFragments`, or `streamChunk`.
- If the current code excerpt contains a generic local name, do not silently repeat that name in the planned code. Explain that it exists today, then show the planned version with the clearer name.
