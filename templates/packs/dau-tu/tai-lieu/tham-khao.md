# Mẫu này dựa trên

Các dự án mã nguồn mở về AI phân tích thị trường và giao dịch (tra 2026-10-28). Đều là nghiên cứu hoặc giáo dục, không phải lời khuyên đầu tư.

- **TradingAgents** (TauricResearch, UCLA/MIT, [arXiv 2412.20138](https://arxiv.org/abs/2412.20138), [GitHub](https://github.com/TauricResearch/TradingAgents)): mô phỏng công ty giao dịch: nhóm phân tích (cơ bản, tâm lý, tin tức, kỹ thuật), nhà nghiên cứu phe tăng và phe giảm tranh biện, trader quyết định, nhóm rủi ro và quản lý danh mục duyệt. → quy trình `#phan-tich-ma`, skill `phan-tich`, `tranh-bien`.
- **ai-hedge-fund** (virattt, [GitHub](https://github.com/virattt/ai-hedge-fund)): nhiều agent theo phong cách nhà đầu tư cùng agent định giá, tâm lý, cơ bản, kỹ thuật, rủi ro; chỉ giả lập lệnh. → danh mục giấy, agent Quản trị rủi ro giới hạn quy mô.
- **FinRobot** (AI4Finance, [arXiv 2405.14767](https://arxiv.org/abs/2405.14767)): agent viết báo cáo phân tích cổ phiếu theo chuỗi suy luận. → mẫu báo cáo trong `phan-tich/`.
- **FinMem** ([arXiv 2311.13743](https://arxiv.org/abs/2311.13743)): agent giao dịch có trí nhớ nhiều lớp và tự rút kinh nghiệm. → bảng `bai_hoc`, quy trình `#danh-gia`.
- Freqtrade (dry-run), Microsoft Qlib (backtest): chưa đưa vào; khi cần backtest có code thì thêm thành bước riêng.
