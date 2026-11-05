---
key: spec-kit
name: Làm theo spec (Spec Kit)
category: Engineering
description: "Spec-driven development theo GitHub Spec Kit: cài Spec Kit vào project nếu chưa có, viết spec, làm rõ, người duyệt spec; lên plan và tasks, model khác hãng soát độ khớp, người duyệt; rồi code theo tasks và build/test phải đạt. Tài liệu nằm ở specs/NNN-tên/."
input: Tính năng cần làm (kết quả người dùng cần)
inputs:
  - { key: feature, description: "Tính năng cần làm: ai dùng, làm được gì, vì sao", required: true }
  - { key: stack, description: "Công nghệ, ràng buộc kỹ thuật cho bước plan (để trống: theo code hiện có)" }
  - { key: check, description: "Lệnh build/test phải đạt sau khi code, ví dụ: go test ./... (để trống: không chạy)" }
outputs:
  - { key: feature_dir, description: "Thư mục spec của tính năng (specs/NNN-tên)", required: true }
  - { key: changes, description: "Đã làm gì (file, hành vi)", required: true }
roles:
  - key: spec
    name: Viết spec và plan
    hint: product thinking, requirements, design, architecture
    access: edit
    prefer: { tier: strong }
  - key: soat
    name: Soát độ khớp
    hint: design review, finding gaps and contradictions
    access: analyze
    differ_from: [spec]
  - key: code
    name: Code theo tasks
    hint: changing code, tests
    access: edit
limits: { turns: 12, timeout: 6h, idle: 45m }
steps:
  - id: cai-dat
    type: code
    name: Cài Spec Kit vào project
    lang: bash
    timeout_s: 600
    script: |
      # Spec Kit v1.3.0 (github/spec-kit, MIT): .specify/ and the .claude/skills/speckit-* skills.
      # Only adds files; existing ones are left alone. No git branch is created.
      set -e
      export PATH="$HOME/.local/bin:$HOME/.cargo/bin:/opt/homebrew/bin:$PATH"
      if [ ! -f .specify/init-options.json ]; then
        if ! command -v uvx >/dev/null 2>&1; then
          echo "Máy chưa có uvx. Cài uv: curl -LsSf https://astral.sh/uv/install.sh | sh, rồi chạy lại."
          exit 1
        fi
        uvx --from "git+https://github.com/github/spec-kit.git@v1.3.0" specify init --here --force \
          --non-interactive --ignore-agent-tools --integration claude --script sh >/dev/null
        echo "Đã cài Spec Kit: .specify/ và .claude/skills/speckit-*. Nhớ commit các file này."
      fi
      if grep -q '\[PROJECT_NAME\]' .specify/memory/constitution.md 2>/dev/null; then
        echo "constitution:template"
      else
        echo "constitution:set"
      fi
    next: can-hien-phap
    position: { x: 0, y: 0 }
  - id: can-hien-phap
    type: condition
    name: Đã có constitution?
    if: "{{steps.cai-dat.output}} contains constitution:template"
    then: hien-phap
    else: viet-spec
    position: { x: 300, y: 0 }
  - id: hien-phap
    type: agent
    name: Viết constitution
    role: spec
    skill: speckit-constitution
    prompt: |
      The project has no Spec Kit constitution yet (.specify/memory/constitution.md is still the template).
      Write it from what the repository already shows: its README, contributor docs, agent instructions (CLAUDE.md, AGENTS.md), tests and CI. Keep only principles the code and docs support; do not invent rules.
      The office runs this step with no one to answer, so do not ask questions: write the assumptions you made in your answer.
    next: viet-spec
    position: { x: 600, y: -160 }
  - id: viet-spec
    type: agent
    name: Viết spec
    role: spec
    skill: speckit-specify
    prompt: |
      Feature description (this is the input of /speckit-specify):
      {{input.feature}}

      The office runs this step with no one to answer. Do not create a git branch.
      End your answer with the line `FEATURE_DIR: specs/<NNN-name>`.
    next: lam-ro
    position: { x: 900, y: 0 }
  - id: lam-ro
    type: agent
    name: Làm rõ spec
    role: spec
    skill: speckit-clarify
    prompt: |
      Clarify the spec written in the previous step.
      The feature folder is the `FEATURE_DIR:` line of the spec step: {{steps.viet-spec.output}}
      .specify/feature.json is local state and may be missing in your checkout: if so, run the skill's scripts with SPECIFY_FEATURE_DIRECTORY=<that folder>.
      The office runs this step with no one to answer, so do not wait for replies. For each question you would ask, take the recommended answer when it is low-risk and record it in the spec's Clarifications section as an assumption. Leave the ones that change scope, security or data unanswered and list them.
      Your answer goes to the person who approves the spec: give the feature folder, a short summary of the spec, the assumptions you took, and the open questions.
    next: duyet-spec
    position: { x: 1200, y: 0 }
  - id: duyet-spec
    type: approve
    name: Người duyệt spec
    note: |
      Spec của tính năng:
      {{steps.lam-ro.output}}

      Duyệt để lên plan. Bỏ qua nếu spec chưa đúng (chạy lại quy trình với mô tả rõ hơn).
    next: len-plan
    else: dung
    position: { x: 1500, y: 0 }
  - id: len-plan
    type: agent
    name: Lên plan
    role: spec
    skill: speckit-plan
    prompt: |
      Make the technical plan for the approved spec.
      The feature folder is the `FEATURE_DIR:` line of the spec step: {{steps.viet-spec.output}}
      .specify/feature.json is local state and may be missing in your checkout: if so, run the skill's scripts with SPECIFY_FEATURE_DIRECTORY=<that folder>.
      Technology and constraints: {{input.stack}}
      (If empty, follow the stack and conventions the repository already uses.)
      The office runs this step with no one to answer; write what you assumed.
    next: chia-tasks
    position: { x: 1800, y: 0 }
  - id: chia-tasks
    type: agent
    name: Chia tasks
    role: spec
    skill: speckit-tasks
    prompt: |
      Generate tasks.md from the plan.
      The feature folder is the `FEATURE_DIR:` line of the spec step: {{steps.viet-spec.output}}
      .specify/feature.json is local state and may be missing in your checkout: if so, run the skill's scripts with SPECIFY_FEATURE_DIRECTORY=<that folder>.
    next: soat-khop
    position: { x: 2100, y: 0 }
  - id: soat-khop
    type: agent
    name: Soát độ khớp spec, plan, tasks
    role: soat
    skill: speckit-analyze
    prompt: |
      Analyze the consistency of spec.md, plan.md and tasks.md, and against .specify/memory/constitution.md. Read only; change nothing.
      The feature folder is the `FEATURE_DIR:` line of the spec step: {{steps.viet-spec.output}}
      .specify/feature.json is local state and may be missing in your checkout: if so, run the skill's scripts with SPECIFY_FEATURE_DIRECTORY=<that folder>.
      Your report goes to the person who approves the plan: start with the CRITICAL and HIGH findings, then a short summary of the plan and tasks.
    next: duyet-plan
    position: { x: 2400, y: 0 }
  - id: duyet-plan
    type: approve
    name: Người duyệt plan và tasks
    note: |
      Plan và tasks:
      {{steps.chia-tasks.output}}

      Soát độ khớp (model khác hãng):
      {{steps.soat-khop.output}}

      Duyệt để code theo tasks.md.
    next: code
    else: dung
    position: { x: 2700, y: 0 }
  - id: code
    type: agent
    name: Code theo tasks
    role: code
    skill: speckit-implement
    prompt: |
      Implement the approved tasks.md, phase by phase, ticking each task off ([X]) as it is done.
      The feature folder is the `FEATURE_DIR:` line of the spec step: {{steps.viet-spec.output}}
      .specify/feature.json is local state and may be missing in your checkout: if so, run the skill's scripts with SPECIFY_FEATURE_DIRECTORY=<that folder>.
      Findings from the review before approval (fix the CRITICAL and HIGH ones as you go):
      {{steps.soat-khop.output}}

      Run the project's tests as you go. Your answer: what changed (files, behavior), tasks still open and why.
    next: co-kiem-tra
    position: { x: 3000, y: 0 }
  - id: co-kiem-tra
    type: condition
    name: Có lệnh kiểm tra?
    if: "'{{input.check}}' != ''"
    then: kiem-tra
    else: xong
    position: { x: 3300, y: 0 }
  - id: kiem-tra
    type: check
    name: Build/test đạt
    command: "{{input.check}}"
    next: xong
    else: sua
    max_loops: 3
    position: { x: 3600, y: 0 }
  - id: sua
    type: agent
    name: Sửa theo lỗi
    role: code
    prompt: |
      The command `{{input.check}}` does not pass:
      {{steps.kiem-tra.output}}

      Find the root cause and fix the code until it passes. Do not disable tests or ignore errors. Keep tasks.md in step with what is done.
    next: kiem-tra
    position: { x: 3900, y: 160 }
  - id: xong
    type: end
    name: Xong
    summary: "Đã code theo spec.\n{{steps.code.output}}\n{{steps.sua.output}}"
    outputs:
      feature_dir: "{{steps.viet-spec.output}}"
      changes: "{{steps.code.output}}"
    position: { x: 3900, y: -120 }
  - id: dung
    type: end
    name: Dừng
    summary: "Dừng ở bước duyệt; spec và plan đã viết vẫn nằm trong specs/.\n{{steps.lam-ro.output}}"
    outputs:
      feature_dir: "{{steps.viet-spec.output}}"
      changes: "Chưa code."
    position: { x: 1800, y: 240 }
---
The workflow runs by the steps in the header (steps). Each step loads the Spec Kit skill of its phase (`.claude/skills/speckit-*`), which the first step installs into the project when it is missing.
