mod cli;
use colored::Colorize;
use std::sync::atomic::{AtomicBool, Ordering};

/// We define a global debug property so that we can access it from anywhere.
/// This is used in a bunch of places to change the behavior of the program.
///
/// This is critical for the way that error reporting/printing works as the <@ file:line>
/// functionality is only output from reading the value of this global.
pub static DEBUG: AtomicBool = AtomicBool::new(false);

pub fn debug_enabled() -> bool {
    DEBUG.load(Ordering::Relaxed)
}

fn main() {
    let mut cli = match cli::Cli::new() {
        Ok(cli) => cli,
        Err(e) => {
            polyfmt::println!("{} {:?}", "x".red(), e);
            polyfmt::finish!();
            std::process::exit(1)
        }
    };

    match cli.run() {
        Ok(_) => {}
        Err(e) => {
            // Before we start printing the error tree we need to make sure the previous fmtter is properly cleaned up.
            polyfmt::finish!();

            let mut fmt = polyfmt::new(polyfmt::Format::Tree, polyfmt::Options::default());
            fmt.println(&format!("{} {}: {e}", "x".red(), "Error".bold()));

            let _indent_guard = fmt.indent();
            for (i, cause) in e.chain().skip(1).enumerate() {
                fmt.println(&format!("{i}: {cause}"));
            }

            fmt.finish();
            std::process::exit(1)
        }
    }
}
