// Office adaptations of imported templates. Keep these in the importer so a
// sync cannot silently restore incompatible instructions or vague summaries.
const descriptions = {
  'aitmpl/architect-review': 'Review code changes for SOLID, layering and maintainability; use for consistency within an existing architecture.',
  'aitmpl/architect-reviewer': 'Evaluate system-wide architecture, technology choices and service boundaries before major refactors.',
  'aitmpl/code-architect': 'Design a new feature with a named architecture decision, alternatives, security checklist and detailed implementation map.',
  'ecc/code-architect': 'Plan a new feature around existing patterns, with concrete interfaces, data flow and a concise build sequence.',
  'aitmpl/code-explorer': 'Investigate an existing feature in depth: execution paths, abstractions, integration points and extension opportunities.',
  'ecc/code-explorer': 'Map an existing feature using a structured report of entry points, execution flow, layers, key files and dependencies.',
  'aitmpl/code-reviewer': 'Review a diff with automated pre-checks and language-specific checks for security, correctness, tests and maintainability.',
  'ecc/code-reviewer': 'Review changes with confidence filtering and proof for serious findings; includes React/Next.js and Node.js checks.',
  'aitmpl/code-simplifier': 'Refine recently changed code using project standards, explicit control flow and a balance between brevity and clarity.',
  'ecc/code-simplifier': 'Simplify changed code through a short checklist for nesting, duplication, naming and behavior preservation.',
  'aitmpl/planner': 'Create a concise implementation plan after researching the codebase; keep planning separate from implementation.',
  'ecc/planner': 'Plan complex features in phases with file-level steps, dependencies, testing strategy, risks and success criteria.',
  'aitmpl/seo-specialist': 'Run a broad SEO assessment covering technical issues, keyword strategy, competitors and AI search visibility.',
  'ecc/seo-specialist': 'Audit technical and on-page SEO with severity-ranked findings, evidence, recommended fixes and verification.',
  'agency/carousel-growth-engine': 'Prepare TikTok and Instagram carousels and measure their results; publish or schedule only within the user’s explicit authorization.'
}

const descriptionsVi = {
  'aitmpl/architect-review': 'Soát thay đổi theo SOLID, phân lớp và khả năng bảo trì; chọn khi cần giữ nhất quán với kiến trúc hiện có.',
  'aitmpl/architect-reviewer': 'Đánh giá kiến trúc toàn hệ thống, công nghệ và ranh giới dịch vụ trước khi tái cấu trúc lớn.',
  'aitmpl/code-architect': 'Thiết kế tính năng mới với quyết định kiến trúc, phương án so sánh, checklist bảo mật và bản đồ triển khai chi tiết.',
  'ecc/code-architect': 'Lập thiết kế ngắn gọn theo cấu trúc code hiện có: giao diện thành phần, luồng dữ liệu và thứ tự triển khai.',
  'aitmpl/code-explorer': 'Khảo sát sâu tính năng hiện có: đường chạy, các lớp trừu tượng, điểm tích hợp và cơ hội mở rộng.',
  'ecc/code-explorer': 'Lập báo cáo có cấu trúc về điểm vào, đường chạy, các lớp kiến trúc, file chính và phụ thuộc của tính năng.',
  'aitmpl/code-reviewer': 'Soát diff với kiểm tra tự động ban đầu và checklist theo ngôn ngữ: bảo mật, tính đúng, test và khả năng bảo trì.',
  'ecc/code-reviewer': 'Soát thay đổi theo độ chắc chắn của phát hiện; lỗi nặng phải có bằng chứng, kèm checklist React/Next.js và Node.js.',
  'aitmpl/code-simplifier': 'Tinh gọn code vừa sửa theo quy ước project, ưu tiên luồng xử lý rõ ràng và cân bằng giữa ngắn gọn với dễ đọc.',
  'ecc/code-simplifier': 'Tinh gọn code vừa sửa bằng checklist ngắn về phân nhánh lồng, trùng lặp, tên gọi và giữ nguyên hành vi.',
  'aitmpl/planner': 'Khảo sát code rồi lập kế hoạch triển khai ngắn gọn; tách bước lên kế hoạch khỏi việc sửa code.',
  'ecc/planner': 'Chia tính năng phức tạp thành các giai đoạn, chỉ rõ file, phụ thuộc, cách kiểm thử, rủi ro và tiêu chí xong.',
  'aitmpl/seo-specialist': 'Đánh giá SEO toàn diện: kỹ thuật, từ khóa, đối thủ và khả năng xuất hiện trong kết quả tìm kiếm AI.',
  'ecc/seo-specialist': 'Kiểm tra SEO kỹ thuật và nội dung trang; xếp mức nghiêm trọng, dẫn chứng, cách sửa và cách xác nhận.',
  'agency/carousel-growth-engine': 'Chuẩn bị bài nhiều ảnh cho TikTok/Instagram và đo kết quả; chỉ đăng hoặc hẹn lịch trong phạm vi người dùng đã cho phép rõ ràng.'
}

export function agentDescription(set, key, original) {
  return descriptions[`${set}/${key}`] ?? original
}

export function agentDescriptionVi(set, key) {
  return descriptionsVi[`${set}/${key}`]
}

export function agencyBody(key, source) {
  // Output contracts often live in fences too. Preserve prose and structured
  // formats; omit only executable examples, as the original importer intended.
  let body = source.replace(/^[ \t]*(`{3,}|~{3,})([^\n]*)\n([\s\S]*?)^[ \t]*\1[ \t]*\r?$/gm,
    (block, fence, info) => /^(?:markdown|md|mdx|text|plaintext|json|jsonc|yaml|yml|toml)?$/i.test(info.trim()) ? block : '')
  if (key === 'carousel-growth-engine') {
    body = body
      .replace('## Tool Stack & APIs',
        '## Tool Stack & APIs\n\nBefore starting, check which scripts, tools and account connections actually exist in the project. This template does not install them. Use configured equivalents when available; otherwise prepare the content brief and explain the missing dependency. Never invent a successful tool call or publication.')
      .replace('You never ask for permission between steps — you research, generate, verify, publish, and learn, then report back with results.',
        'Research, draft and verify within the assigned scope. Publishing, spending money, contacting third parties and scheduling require the user’s explicit authorization and the tools and permissions configured in Office. Report completed work and anything still awaiting authorization.')
      .replace('- **Zero Confirmation**: Run the entire pipeline without asking for user approval between steps',
        '- **Authorization**: Prepare drafts independently. Publish or schedule only when the user has explicitly authorized the action, target accounts and scope; otherwise return the prepared assets for review.')
      .replace('- **Notify Only at End**: The user sees results (published URLs), not process updates',
        '- **Progress and Results**: Follow Office’s progress reporting rules. Distinguish drafts, authorized publications and actions still awaiting authorization.')
      .replace('- **Self-Schedule**: Read `learnings.json` bestTimes and schedule next execution at the optimal posting time',
        '- **Scheduling**: Propose an Office automation only when the user asks for recurring publication; use the configured authorization flow.')
      .replace('5. **Self-Schedule**: Read `learnings.json` bestTimes and set next cron execution at the optimal hour',
        '5. **Scheduling**: If the user requests recurring publication, propose the cadence through Office automation; otherwise stop after the assigned work.')
      .replace('### Phase 4: Publish & Track',
        '### Phase 4: Publish & Track\n\nProceed only if the user has explicitly authorized publication to these accounts. Without that authorization, return the draft assets and proposed caption instead. Check that the required scripts, account credentials and configured tools exist; do not assume this template installs them.')
      .replace('Drive consistent social media growth through autonomous carousel publishing:',
        'Prepare carousels for the agreed publishing workflow:')
      .replace('— every single day', '— at the cadence agreed with the user')
      .replace('- **Storage**: `/tmp/carousel/learnings.json` — persistent knowledge base updated after every post',
        '- **Storage**: Keep learning records in the project’s established data directory; temporary folders are not persistent storage.')
      .replace('**Publishing Consistency**: 1 carousel per day, every day, fully autonomous',
        '**Publishing Consistency**: Follow the cadence and accounts explicitly authorized by the user')
      .replace('Your job is to publish one carousel every day, learn from every single post, and make the next one better.',
        'Prepare carousels for the agreed cadence, publish only within explicit authorization, and learn from each authorized post.')
  }
  return body.replace(/\n{3,}/g, '\n\n').trim()
}
