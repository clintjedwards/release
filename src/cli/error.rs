use colored::Colorize;
use rootcause::{
    ReportRef,
    handlers::FormattingFunction,
    hooks::{Hooks, report_formatter::ReportFormatter},
    markers::{Dynamic, Local, Uncloneable},
};

#[derive(Debug, Clone, Copy)]
pub struct TreeFormatter;

impl TreeFormatter {
    fn linear_chain(
        mut r: ReportRef<'_, Dynamic, Uncloneable, Local>,
    ) -> Vec<ReportRef<'_, Dynamic, Uncloneable, Local>> {
        let mut out = Vec::new();
        loop {
            out.push(r);

            // rootcause supports branching, but your case is a single-child chain.
            let next = r.children().iter().next().map(|c| c.into_uncloneable());
            match next {
                Some(n) => r = n,
                None => break,
            }
        }
        out
    }

    fn write_prefixed_lines(
        f: &mut std::fmt::Formatter<'_>,
        first_prefix: &str,
        cont_prefix: &str,
        text: &str,
    ) -> std::fmt::Result {
        let mut lines = text.lines();
        if let Some(first) = lines.next() {
            writeln!(f, "{first_prefix}{first}")?;
        }
        for line in lines {
            writeln!(f, "{cont_prefix}{line}")?;
        }
        Ok(())
    }

    fn format_one(
        f: &mut std::fmt::Formatter<'_>,
        r: ReportRef<'_, Dynamic, Uncloneable, Local>,
        i: usize,
        n: usize,
        _report_formatting_function: FormattingFunction,
    ) -> std::fmt::Result {
        let is_first = i == 0;
        let is_last = i + 1 == n;
        let attachments: Vec<_> = r.attachments().iter().collect();
        let has_attachments = !attachments.is_empty();

        // “Square” corners:
        // top:    ┌
        // middle: ├
        // last:   └
        let head = if is_first && !is_last {
            "┌─ "
        } else if is_last && has_attachments {
            "├─ "
        } else if is_last {
            "└─ "
        } else {
            "├─ "
        };

        // Continuation prefix for multi-line contexts at same node.
        let head_cont = if is_last && !has_attachments {
            "   "
        } else {
            "│  "
        };

        // 1) Context
        let ctx = format!("{}", r.format_current_context_unhooked());
        let head_col = head.magenta().to_string();
        let head_cont_col = head_cont.magenta().to_string();
        Self::write_prefixed_lines(f, &head_col, &head_cont_col, &ctx)?;

        // 2) Attachments (if present)
        for (ai, attachment) in attachments.iter().enumerate() {
            let is_last_attachment = ai + 1 == attachments.len();
            let is_final_line = is_last && is_last_attachment;
            let attachment_head = if is_final_line {
                "└── "
            } else {
                "├── "
            };
            let attachment_cont = if is_final_line { "    " } else { "│   " };
            let text = format!("{attachment}");
            let attachment_head_col = attachment_head.magenta().to_string();
            let attachment_cont_col = attachment_cont.magenta().to_string();
            Self::write_prefixed_lines(f, &attachment_head_col, &attachment_cont_col, &text)?;
        }

        // Optional blank “tree spacer” line between nodes (except after last)
        if !is_last {
            writeln!(f, "{}", "┆".magenta())?;
        }

        Ok(())
    }
}

impl ReportFormatter for TreeFormatter {
    fn format_reports(
        &self,
        reports: &[ReportRef<'_, Dynamic, Uncloneable, Local>],
        f: &mut std::fmt::Formatter<'_>,
        report_formatting_function: FormattingFunction,
    ) -> std::fmt::Result {
        for (ri, root) in reports.iter().enumerate() {
            if ri > 0 {
                writeln!(f)?;
            }

            let chain = Self::linear_chain(*root);
            for (i, r) in chain.iter().copied().enumerate() {
                Self::format_one(f, r, i, chain.len(), report_formatting_function)?;
            }
        }
        Ok(())
    }
}

/// This changes the default rootcause formatter into one that's a bit more aesthetically pleasing, using box drawing
/// to create a tree instead of using bullet points.
pub fn alter_error_formatter(debug: bool) {
    if debug {
        Hooks::new()
            .report_formatter(TreeFormatter)
            .install()
            .expect("failed to install rootcause hooks");
        return;
    }

    Hooks::new_without_locations()
        .report_formatter(TreeFormatter)
        .install()
        .expect("failed to install rootcause hooks");
}
