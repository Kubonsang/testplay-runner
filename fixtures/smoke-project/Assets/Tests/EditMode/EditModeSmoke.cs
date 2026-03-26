using NUnit.Framework;

namespace FastPlaySmoke
{
    public class EditModeSmoke
    {
        [Test]
        public void AlwaysPasses()
        {
            Assert.AreEqual(2, 1 + 1, "Basic arithmetic must hold in EditMode");
        }
    }
}
